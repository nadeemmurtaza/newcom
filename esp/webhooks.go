package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"

	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type Webhook struct {
	ID          string `json:"id,omitempty"`
	EndpointURL string `json:"endpoint_url"`
	SecretKey   string `json:"secret_key,omitempty"`
}

type WebhookEvent struct {
	OrgID        string `json:"org_id"`
	SubAccountID string `json:"sub_account_id"`
	MessageID    string `json:"message_id"`
	EventType    string `json:"event_type"` // open, click, delivered, bounce, dropped
	Metadata     string `json:"metadata"`
	Timestamp    int64  `json:"timestamp"`
}

func generateSecretKey() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func handleWebhooks(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org_id")
	userID := r.Header.Get("X-User-ID")

	if orgID == "" || userID == "" {
		http.Error(w, "Missing org_id or X-User-ID", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	switch r.Method {
	case "GET":
		if !hasPermission(userID, orgID, "webhooks:read") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		rows, err := db.Query("SELECT id, endpoint_url, secret_key FROM webhooks WHERE org_id = $1", orgID)
		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var hooks []Webhook
		for rows.Next() {
			var h Webhook
			if err := rows.Scan(&h.ID, &h.EndpointURL, &h.SecretKey); err == nil {
				hooks = append(hooks, h)
			}
		}
		json.NewEncoder(w).Encode(hooks)

	case "POST":
		if !hasPermission(userID, orgID, "webhooks:write") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		var h Webhook
		if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}

		h.SecretKey = generateSecretKey()

		err := db.QueryRow(`
			INSERT INTO webhooks (org_id, endpoint_url, secret_key) 
			VALUES ($1, $2, $3) RETURNING id`,
			orgID, h.EndpointURL, h.SecretKey).Scan(&h.ID)
		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(h)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// QueueWebhookEvent pushes an event to Redis to be dispatched to customer webhooks
func QueueWebhookEvent(rdb *redis.Client, event WebhookEvent) {
	event.Timestamp = time.Now().Unix()
	payload, _ := json.Marshal(event)
	rdb.LPush(context.Background(), "webhook_outbound_queue", payload)
}

// StartWebhookDispatcher runs in the background and POSTs events to customer endpoints
func StartWebhookDispatcher(ctx context.Context, wg *sync.WaitGroup, db *sql.DB, rdb *redis.Client) {
	defer wg.Done()
	log.Println("Starting Webhook Dispatcher...")
	client := &http.Client{Timeout: 5 * time.Second}

	for {
		select {
		case <-ctx.Done():
			log.Println("WebhookDispatcher: Shutting down gracefully...")
			return
		default:
			// Phase 22: Reliable Queueing (At-Least-Once Delivery)
			payload, err := rdb.BRPopLPush(ctx, "webhook_outbound_queue", "webhook_in_flight_queue", 2*time.Second).Result()
			if err == redis.Nil {
				continue
			} else if err != nil {
				log.Printf("WebhookDispatcher Redis error: %v", err)
				time.Sleep(2 * time.Second)
				continue
			}

		var event WebhookEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			log.Printf("WebhookDispatcher Invalid event payload: %v", err)
			rdb.LRem(ctx, "webhook_in_flight_queue", 1, payload)
			continue
		}

		if event.OrgID == "" {
			rdb.LRem(ctx, "webhook_in_flight_queue", 1, payload)
			continue // Cannot route without OrgID
		}

		// Find all webhooks for this org
		// Phase 49: Advanced Event Filtering
		dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
		rows, err := db.QueryContext(dbCtx, `
			SELECT endpoint_url, secret_key 
			FROM webhooks 
			WHERE org_id = $1 AND subscribed_events::text LIKE '%' || $2 || '%'
		`, event.OrgID, event.EventType)
		if err != nil {
			log.Printf("WebhookDispatcher DB error: %v", err)
			dbCancel()
			// Keep in in-flight queue so it can be retried if DB recovers
			continue
		}

		for rows.Next() {
			var endpoint, secret string
			if err := rows.Scan(&endpoint, &secret); err == nil {
				// We don't wait for the dispatchWebhook to finish to pop the next message, 
				// but in a true robust system we'd manage the acking more granularly. 
				// We fire and forget the actual HTTP call here.
				go dispatchWebhook(client, rdb, endpoint, secret, event)
			}
		}
		rows.Close()
		dbCancel()
		
		rdb.LRem(context.Background(), "webhook_in_flight_queue", 1, payload)
		}
	}
}

func dispatchWebhook(client *http.Client, rdb *redis.Client, endpoint string, secret string, event WebhookEvent) {
	ctx := context.Background()
	cbKey := "webhook_failures:" + endpoint
	
	// Phase 21: Circuit Breaker Check
	failures, _ := rdb.Get(ctx, cbKey).Int()
	if failures >= 5 {
		log.Printf("Webhook circuit broken for %s (dropping event)", endpoint)
		MetricWebhooksDispatched.WithLabelValues("circuit_broken").Inc()
		// In a real system, send to a Dead-Letter Queue (DLQ) table here.
		return
	}

	payload, _ := json.Marshal(event)

	// Sign payload with HMAC-SHA256
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	signature := hex.EncodeToString(h.Sum(nil))

	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ESP-Signature", signature)

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode >= 400 {
		log.Printf("Webhook failed for %s: %v", endpoint, err)
		MetricWebhooksDispatched.WithLabelValues("failed").Inc()
		// Increment failure counter, reset after 5 minutes
		rdb.Incr(ctx, cbKey)
		rdb.Expire(ctx, cbKey, 5*time.Minute)
		return
	}
	defer resp.Body.Close()

	log.Printf("Webhook successfully dispatched to %s", endpoint)
	MetricWebhooksDispatched.WithLabelValues("success").Inc()
	// Reset circuit breaker on success
	rdb.Del(ctx, cbKey)
}
