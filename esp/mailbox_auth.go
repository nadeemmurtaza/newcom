package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Phase 52: Mailbox-Level Session Auth
//
// Every /api/mailbox/* endpoint (read messages, star, move, snooze, label,
// pin, ignore thread, recall, delete) previously trusted whatever
// mailbox_id/message_id was in the URL or query string with zero auth: any
// caller who guessed or observed a UUID could read or mutate any mailbox in
// the system. This is the fix -- a mailbox logs in with its email + app
// password (bcrypt-checked against app_passwords), gets a short-lived
// session token, and every handler below verifies that token owns the
// mailbox/message it's touching before doing anything.

const mailboxSessionTTL = 24 * time.Hour

// handleMailboxLogin exchanges an email + app password for a session token.
// POST /api/mailbox/login  body: {"email_address": "...", "app_password": "..."}
func handleMailboxLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EmailAddress string `json:"email_address"`
		AppPassword  string `json:"app_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var mailboxID string
	if err := db.QueryRow("SELECT id FROM mailboxes WHERE email_address = $1", strings.ToLower(req.EmailAddress)).Scan(&mailboxID); err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	rows, err := db.Query("SELECT id, token_hash FROM app_passwords WHERE mailbox_id = $1", mailboxID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var matchedID string
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.AppPassword)) == nil {
			matchedID = id
			break
		}
	}
	if matchedID == "" {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}
	token := hex.EncodeToString(rawToken)

	if err := rdb.Set(r.Context(), "mailbox_session:"+token, mailboxID, mailboxSessionTTL).Err(); err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}
	db.Exec("UPDATE app_passwords SET last_used_at = NOW() WHERE id = $1", matchedID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"token":      token,
		"mailbox_id": mailboxID,
		"expires_in": int(mailboxSessionTTL.Seconds()),
	})
}

type mailboxSessionContextKey struct{}

// mailboxAuthMiddleware resolves "Authorization: Bearer <token>" to a
// mailbox ID via Redis and makes it available to the wrapped handler.
func mailboxAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "Unauthorized: Missing session token", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == "" {
			http.Error(w, "Unauthorized: Missing session token", http.StatusUnauthorized)
			return
		}

		mailboxID, err := rdb.Get(r.Context(), "mailbox_session:"+token).Result()
		if err != nil || mailboxID == "" {
			http.Error(w, "Unauthorized: Invalid or expired session", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), mailboxSessionContextKey{}, mailboxID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func sessionMailboxID(r *http.Request) string {
	if v, ok := r.Context().Value(mailboxSessionContextKey{}).(string); ok {
		return v
	}
	return ""
}

// requireMailboxOwnership 403s unless the authenticated session's mailbox
// matches candidateMailboxID. Call after resolving which mailbox a
// message/resource belongs to.
func requireMailboxOwnership(w http.ResponseWriter, r *http.Request, candidateMailboxID string) bool {
	if candidateMailboxID == "" || candidateMailboxID != sessionMailboxID(r) {
		http.Error(w, "Forbidden: You do not own this mailbox", http.StatusForbidden)
		return false
	}
	return true
}

// sessionOrgID resolves the org that owns the session's mailbox. mailboxes
// has no RLS policy, so this is a plain read.
func sessionOrgID(r *http.Request) (string, error) {
	var orgID string
	err := db.QueryRow("SELECT org_id FROM mailboxes WHERE id = $1", sessionMailboxID(r)).Scan(&orgID)
	return orgID, err
}

// mailboxOfMessage looks up which mailbox a message belongs to, scoped to
// the requesting session's org. messages carries a tenant_isolation_policy
// RLS policy keyed on app.current_org_id (see persistInboundMessage in
// inbound_smtp.go) -- a plain, unscoped SELECT against it is always denied
// regardless of caller, so this must run inside a transaction that sets
// that session variable first, exactly like the write path does.
func mailboxOfMessage(r *http.Request, messageID string) (string, error) {
	orgID, err := sessionOrgID(r)
	if err != nil {
		return "", err
	}

	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("SELECT set_config('app.current_org_id', $1, true)", orgID); err != nil {
		return "", err
	}

	var mailboxID string
	if err := tx.QueryRow("SELECT mailbox_id FROM messages WHERE id = $1", messageID).Scan(&mailboxID); err != nil {
		return "", err
	}
	return mailboxID, tx.Commit()
}

// withOrgTx runs fn inside a transaction with app.current_org_id set to the
// session's org, for any UPDATE/DELETE against messages (not just the SELECT
// lookups above) -- the RLS policy's USING clause governs those too, so a
// plain db.Exec against messages is blocked exactly like a plain SELECT.
func withOrgTx(r *http.Request, fn func(tx *sql.Tx) error) error {
	orgID, err := sessionOrgID(r)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("SELECT set_config('app.current_org_id', $1, true)", orgID); err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// threadAndMailboxOfMessage is mailboxOfMessage plus the thread_id, for
// handlers that need both (e.g. handleIgnoreThread).
func threadAndMailboxOfMessage(r *http.Request, messageID string) (threadID, mailboxID string, err error) {
	orgID, err := sessionOrgID(r)
	if err != nil {
		return "", "", err
	}

	tx, err := db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("SELECT set_config('app.current_org_id', $1, true)", orgID); err != nil {
		return "", "", err
	}

	if err := tx.QueryRow("SELECT COALESCE(thread_id, ''), mailbox_id FROM messages WHERE id = $1", messageID).Scan(&threadID, &mailboxID); err != nil {
		return "", "", err
	}
	return threadID, mailboxID, tx.Commit()
}
