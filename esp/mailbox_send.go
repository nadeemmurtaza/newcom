package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

// Phase 62: Sending Mail From a Logged-In Mailbox
//
// /v1/send (handleSendEmail) requires authMiddleware -- an org API key.
// That's the right gate for a programmatic integration, but it means a
// person who just logged into their own mailbox (nID or password session,
// then a mailbox session via handleCreateMailboxSession/handleMailboxLogin)
// had no way to actually send mail at all: a webmail inbox you can only
// read is not a mail client. This reuses the exact same outbound queue
// handleSendEmail writes to, scoped to the session's own mailbox -- From
// is always that mailbox's real address, never a value the client
// supplies, so nobody can send mail claiming to be a mailbox they don't
// own.
// POST /api/mailbox/send
func handleMailboxSend(w http.ResponseWriter, r *http.Request) {
	mailboxID := sessionMailboxID(r)

	var fromAddress string
	orgID, err := sessionOrgID(r)
	if err != nil {
		http.Error(w, "Mailbox not found", http.StatusNotFound)
		return
	}
	if err := db.QueryRow("SELECT email_address FROM mailboxes WHERE id = $1", mailboxID).Scan(&fromAddress); err != nil {
		http.Error(w, "Mailbox not found", http.StatusNotFound)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if cachedResponse, exists := idempotencyEngine.Check(r.Context(), idempotencyKey); exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(cachedResponse))
		return
	}

	var req struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.To == "" || req.Subject == "" || req.Body == "" {
		http.Error(w, "to, subject and body are required", http.StatusBadRequest)
		return
	}

	if !mailboxManager.CheckVelocityLimit(r.Context(), mailboxID) {
		http.Error(w, "429 Too Many Requests: Mailbox daily send limit exceeded", http.StatusTooManyRequests)
		return
	}

	msgID := "msg_" + fmt.Sprintf("%d", time.Now().UnixNano())
	outReq := SendEmailRequest{
		MessageID: msgID,
		OrgID:     orgID,
		To:        req.To,
		From:      fromAddress,
		Subject:   req.Subject,
		Body:      req.Body,
	}

	log.Printf("Mailbox send: %s -> %s", fromAddress, req.To)

	reqJSON, err := json.Marshal(outReq)
	if err != nil {
		http.Error(w, "Failed to serialize request", http.StatusInternalServerError)
		return
	}
	sendScore := float64(time.Now().Add(10 * time.Second).Unix()) // Undo Send window, same as handleSendEmail
	if err := rdb.ZAdd(r.Context(), "email_scheduled_queue", redis.Z{Score: sendScore, Member: reqJSON}).Err(); err != nil {
		log.Printf("Failed to push to scheduled queue: %v", err)
		http.Error(w, "Failed to queue email", http.StatusInternalServerError)
		return
	}

	resp := SendEmailResponse{MessageID: msgID, Status: "QUEUED"}
	respJSON, _ := json.Marshal(resp)
	if idempotencyKey != "" {
		idempotencyEngine.Save(r.Context(), idempotencyKey, string(respJSON))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	w.Write(respJSON)
}
