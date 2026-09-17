package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Phase 27: The Gmail Architecture Convergence

// RawEmail represents the binary blob pushed by the C++ MTA
type RawEmail struct {
	Sender  string `json:"sender"`
	Rcpt    string `json:"rcpt"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Headers map[string]string `json:"headers"`
}

// 2. The Heavy Guard: Async Spam & Malware Filter
func startHeavyGuardWorker(ctx context.Context, wg *sync.WaitGroup, db *sql.DB, rdb *redis.Client) {
	defer wg.Done()
	log.Println("Gmail Pipeline: Heavy Guard (Spam/Malware AI) worker started")
	for {
		select {
		case <-ctx.Done():
			return
		default:
			res, err := rdb.BRPop(ctx, 2*time.Second, "email_inbound_queue").Result()
			if err != nil {
				continue
			}
			
			var email RawEmail
			if err := json.Unmarshal([]byte(res[1]), &email); err != nil {
				// Phase 34: DLQ Routing for Poison Pills
				RouteToDLQ(ctx, rdb, "email_inbound_queue", res[1], "Invalid JSON format")
				continue
			}

			// Simulate Machine Learning Spam Filter
			spamScore := 0
			lowerBody := strings.ToLower(email.Body)
			if strings.Contains(lowerBody, "viagra") { spamScore += 5 }
			if strings.Contains(lowerBody, "wire transfer") { spamScore += 3 }

			email.Headers["X-Spam-Score"] = "0"
			if spamScore >= 5 {
				log.Printf("Heavy Guard: Blocked highly malicious payload from %s", email.Sender)
				email.Headers["X-Spam-Score"] = "5"
			}

			// Push to next stage in pipeline
			cleanBytes, _ := json.Marshal(email)
			rdb.LPush(ctx, "email_clean_queue", cleanBytes)
		}
	}
}

// 3. Threading, 4. Dual-Write Storage, 5. Post-Ingestion Event Stream
func startDeliveryWorker(ctx context.Context, wg *sync.WaitGroup, db *sql.DB, rdb *redis.Client) {
	defer wg.Done()
	log.Println("Gmail Pipeline: Delivery & Dual-Write worker started")
	
	// Ensure simulated Colossus block storage directory exists
	os.MkdirAll("./colossus_blob_storage", 0755)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			res, err := rdb.BRPop(ctx, 2*time.Second, "email_clean_queue").Result()
			if err != nil {
				continue
			}

			var email RawEmail
			if err := json.Unmarshal([]byte(res[1]), &email); err != nil {
				RouteToDLQ(ctx, rdb, "email_clean_queue", res[1], "Invalid JSON format")
				continue
			}

			// 3. Threading Engine (Graph Linking)
			threadID := email.Headers["In-Reply-To"]
			if threadID == "" {
				threadID = uuid.NewString() // Mint new Thread-ID
			}

			// Simulated Recipient Resolution
			var mailboxID, folderID string
			db.QueryRow("SELECT id FROM mailboxes LIMIT 1").Scan(&mailboxID)
			
			folderName := "Inbox"
			if email.Headers["X-Spam-Score"] == "5" {
				folderName = "Spam"
			}
			db.QueryRow("SELECT id FROM folders WHERE mailbox_id = $1 AND name = $2", mailboxID, folderName).Scan(&folderID)

			// 4. Dual-Write Storage Architecture (Spanner + Colossus)
			
			// Write #1: Colossus (Heavy Binary Payload)
			colossusBlobID := uuid.NewString()
			blobPath := filepath.Join("./colossus_blob_storage", colossusBlobID+".raw")
			
			// Phase 33: Enterprise AES-256 Encryption At Rest
			encryptedBody, err := EncryptAES([]byte(email.Body))
			if err != nil {
				log.Printf("Encryption Failed for blob %s: %v", colossusBlobID, err)
				encryptedBody = []byte(email.Body) // Fallback for simulation
			}

			os.WriteFile(blobPath, encryptedBody, 0644)

			// Write #2: Spanner (Ultra-light metadata)
			smartReplies := `["Sounds good!", "I'll take a look.", "Thanks!"]`
			snippet := email.Body
			if len(snippet) > 100 { snippet = snippet[:100] }

			var msgID string
			db.QueryRow(`
				INSERT INTO messages (mailbox_id, folder_id, sender, recipients, subject, snippet, colossus_blob_id, smart_replies, thread_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
				mailboxID, folderID, email.Sender, email.Rcpt, email.Subject, snippet, colossusBlobID, smartReplies, threadID,
			).Scan(&msgID)

			// 5. Post-Ingestion Event Stream (FCM Push Notifications & Search Indexing)
			eventPayload := map[string]string{
				"msg_id": msgID,
				"mailbox_id": mailboxID,
				"action": "NEW_MAIL",
			}
			eventBytes, _ := json.Marshal(eventPayload)
			
			// Publish to Redis Pub/Sub for Node.js (FCM Push) and Go (LSM Indexer) to consume asynchronously
			rdb.Publish(ctx, "email_success_stream", eventBytes)
			
			log.Printf("Gmail Pipeline: Dual-Write complete. Published to FCM Event Stream for %s", msgID)
		}
	}
}

// Phase 28: Near Real-Time Search Indexing (LSM-Tree)
func startSearchIndexerWorker(ctx context.Context, wg *sync.WaitGroup, db *sql.DB, rdb *redis.Client, lsm *LSMTree) {
	defer wg.Done()
	log.Println("Gmail Pipeline: LSM-Tree Search Indexer started")

	pubsub := rdb.Subscribe(ctx, "email_success_stream")
	defer pubsub.Close()

	ch := pubsub.Channel()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			var event map[string]string
			if err := json.Unmarshal([]byte(msg.Payload), &event); err == nil {
				if event["action"] == "NEW_MAIL" {
					msgID := event["msg_id"]
					
					// Fetch Colossus body for indexing (simulated)
					var colossusID string
					if err := db.QueryRow("SELECT colossus_blob_id FROM messages WHERE id = $1", msgID).Scan(&colossusID); err == nil {
						blobPath := filepath.Join("./colossus_blob_storage", colossusID+".raw")
						encryptedBytes, _ := os.ReadFile(blobPath)
						
						// Phase 33: Decrypt at Rest
						bodyBytes, err := DecryptAES(encryptedBytes)
						if err != nil {
							// Fallback if it wasn't encrypted during earlier dev phases
							bodyBytes = encryptedBytes 
						}
						
						// Index into LSM-Tree
						lsm.IndexEmail(msgID, string(bodyBytes))
						log.Printf("LSM Indexer: Indexed %s instantly to MemTable", msgID)
					}
				}
			}
		}
	}
}
