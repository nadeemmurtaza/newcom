package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// A transparent 1x1 GIF
var pixel1x1 = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00,
	0xff, 0xff, 0xff, 0x21, 0xf9, 0x04, 0x01, 0x00, 0x00, 0x00, 0x00, 0x2c, 0x00, 0x00, 0x00, 0x00,
	0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x01, 0x44, 0x00, 0x3b,
}

func handleTrackOpen(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid tracking URL", http.StatusBadRequest)
		return
	}
	messageID := parts[3]

	var subAccountID sql.NullString
	db.QueryRow("SELECT sub_account_id FROM events WHERE message_id = $1 LIMIT 1", messageID).Scan(&subAccountID)

	meta := `{"user_agent": "` + r.UserAgent() + `"}`
	_, err := db.Exec("INSERT INTO events (sub_account_id, message_id, event_type, metadata) VALUES ($1, $2, 'open', $3)", subAccountID, messageID, meta)
	if err != nil {
		log.Printf("Failed to log open event: %v", err)
	}

	if subAccountID.Valid {
		var orgID string
		if err := db.QueryRow("SELECT org_id FROM sub_accounts WHERE id = $1", subAccountID.String).Scan(&orgID); err == nil {
			QueueWebhookEvent(rdb, WebhookEvent{OrgID: orgID, SubAccountID: subAccountID.String, MessageID: messageID, EventType: "open", Metadata: meta})
		}
	}

	w.Header().Set("Content-Type", "image/gif")
	w.Write(pixel1x1)
}

func handleTrackClick(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid tracking URL", http.StatusBadRequest)
		return
	}
	messageID := parts[3]
	targetURL := r.URL.Query().Get("url")
	if targetURL == "" {
		http.Error(w, "Missing URL", http.StatusBadRequest)
		return
	}

	var subAccountID sql.NullString
	db.QueryRow("SELECT sub_account_id FROM events WHERE message_id = $1 LIMIT 1", messageID).Scan(&subAccountID)

	meta := `{"url": "` + targetURL + `"}`
	_, err := db.Exec("INSERT INTO events (sub_account_id, message_id, event_type, metadata) VALUES ($1, $2, 'click', $3)", subAccountID, messageID, meta)
	if err != nil {
		log.Printf("Failed to log click event: %v", err)
	}

	if subAccountID.Valid {
		var orgID string
		if err := db.QueryRow("SELECT org_id FROM sub_accounts WHERE id = $1", subAccountID.String).Scan(&orgID); err == nil {
			QueueWebhookEvent(rdb, WebhookEvent{OrgID: orgID, SubAccountID: subAccountID.String, MessageID: messageID, EventType: "click", Metadata: meta})
		}
	}

	http.Redirect(w, r, targetURL, http.StatusFound)
}

type BounceWebhookPayload struct {
	Email  string `json:"email"`
	Reason string `json:"reason"`
	Type   string `json:"type"` 
	OrgID  string `json:"org_id"`
}

func handleBounceWebhook(w http.ResponseWriter, r *http.Request) {
	var payload BounceWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if payload.Type == "hard" && payload.OrgID != "" {
		_, err := db.Exec("INSERT INTO suppression_list (org_id, email, reason) VALUES ($1, $2, $3) ON CONFLICT (org_id, email) DO NOTHING", payload.OrgID, payload.Email, payload.Reason)
		if err != nil {
			log.Printf("Failed to add to suppression list: %v", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		log.Printf("Added %s to suppression list for org %s", payload.Email, payload.OrgID)
	}

	meta := `{"email": "` + payload.Email + `", "reason": "` + payload.Reason + `"}`
	_, err := db.Exec("INSERT INTO events (message_id, event_type, metadata) VALUES ($1, 'bounce', $2)", "unknown", meta)
	if err != nil {
		log.Printf("Failed to log bounce event: %v", err)
	}
	
	if payload.OrgID != "" {
		// We don't have subAccountID for external bounces easily without joining the original message.
		QueueWebhookEvent(rdb, WebhookEvent{OrgID: payload.OrgID, MessageID: "unknown", EventType: "bounce", Metadata: meta})
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
