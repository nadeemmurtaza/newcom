package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

type MailboxMessage struct {
	ID           string   `json:"id"`
	Sender       string   `json:"sender"`
	Subject      string   `json:"subject"`
	BodyText     string   `json:"body_text"`
	BodyHTML     string   `json:"body_html"`
	IsRead       bool     `json:"is_read"`
	IsStarred    bool     `json:"is_starred"`
	CreatedAt    string   `json:"created_at"`
	ThreadID     string   `json:"thread_id,omitempty"`
	Category     string   `json:"category,omitempty"`
	SnoozedUntil string   `json:"snoozed_until,omitempty"`
}

func handleGetMessages(w http.ResponseWriter, r *http.Request) {
	// In production, mailboxID should be resolved from the authenticated User/Token
	mailboxID := r.URL.Query().Get("mailbox_id")
	folderName := r.URL.Query().Get("folder")
	
	if mailboxID == "" {
		http.Error(w, "Missing mailbox_id", http.StatusBadRequest)
		return
	}
	if folderName == "" {
		folderName = "INBOX" // default system folder name is upper-case; see defaultSystemFolders
	}

	// messages is RLS-protected on app.current_org_id (see persistInboundMessage
	// in inbound_smtp.go); resolve the mailbox's org and scope the read to a
	// transaction with that setting, or every query here returns zero rows.
	var orgID string
	if err := db.QueryRow("SELECT org_id FROM mailboxes WHERE id = $1", mailboxID).Scan(&orgID); err != nil {
		http.Error(w, "Mailbox not found", http.StatusNotFound)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec("SELECT set_config('app.current_org_id', $1, true)", orgID); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	rows, err := tx.Query(`
		SELECT m.id, m.sender, m.subject, COALESCE(m.body_text, ''), COALESCE(m.body_html, ''), m.is_read, m.is_starred, m.created_at, COALESCE(m.thread_id, ''), COALESCE(m.category, ''), COALESCE(CAST(m.snoozed_until AS VARCHAR), '')
		FROM messages m
		JOIN folders f ON m.folder_id = f.id
		WHERE m.mailbox_id = $1 AND f.name = $2 AND m.snoozed_until IS NULL
		ORDER BY m.created_at DESC
	`, mailboxID, folderName)

	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var msgs []MailboxMessage
	for rows.Next() {
		var m MailboxMessage
		if err := rows.Scan(&m.ID, &m.Sender, &m.Subject, &m.BodyText, &m.BodyHTML, &m.IsRead, &m.IsStarred, &m.CreatedAt, &m.ThreadID, &m.Category, &m.SnoozedUntil); err == nil {
			msgs = append(msgs, m)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(msgs)
}

func handleToggleStar(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	messageID := parts[4] // /api/mailbox/messages/{id}/star
	
	_, err := db.Exec("UPDATE messages SET is_starred = NOT is_starred WHERE id = $1", messageID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
}

func handleMoveMessage(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	messageID := parts[4] // /api/mailbox/messages/{id}/move
	folderName := r.URL.Query().Get("folder")
	
	if folderName == "" {
		http.Error(w, "Missing folder parameter", http.StatusBadRequest)
		return
	}

	// Resolve the target folder ID (or create it if standard folder missing)
	var folderID string
	var mailboxID string
	err := db.QueryRow("SELECT mailbox_id FROM messages WHERE id = $1", messageID).Scan(&mailboxID)
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}
	
	err = db.QueryRow("SELECT id FROM folders WHERE mailbox_id = $1 AND name = $2", mailboxID, folderName).Scan(&folderID)
	if err != nil {
		err = db.QueryRow("INSERT INTO folders (mailbox_id, name) VALUES ($1, $2) RETURNING id", mailboxID, folderName).Scan(&folderID)
		if err != nil {
			http.Error(w, "Database error creating folder", http.StatusInternalServerError)
			return
		}
	}
	
	_, err = db.Exec("UPDATE messages SET folder_id = $1 WHERE id = $2", folderID, messageID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
}

func handleSnoozeMessage(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	messageID := parts[4] // /api/mailbox/messages/{id}/snooze
	
	// Default to snooze for 24 hours
	_, err := db.Exec("UPDATE messages SET snoozed_until = NOW() + INTERVAL '1 day' WHERE id = $1", messageID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func handleApplyLabel(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	messageID := parts[4] // /api/mailbox/messages/{id}/label
	labelID := r.URL.Query().Get("label_id")
	if labelID == "" {
		http.Error(w, "Missing label_id", http.StatusBadRequest)
		return
	}
	
	_, err := db.Exec("INSERT INTO message_labels (message_id, label_id) VALUES ($1, $2) ON CONFLICT DO NOTHING", messageID, labelID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func handlePinMessage(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	messageID := parts[4]
	db.Exec("UPDATE messages SET is_pinned = NOT is_pinned WHERE id = $1", messageID)
	w.WriteHeader(http.StatusOK)
}

func handleIgnoreThread(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	messageID := parts[4]
	
	// Get thread ID and mailbox ID
	var threadID, mailboxID string
	err := db.QueryRow("SELECT thread_id, mailbox_id FROM messages WHERE id = $1", messageID).Scan(&threadID, &mailboxID)
	if err == nil && threadID != "" {
		db.Exec("INSERT INTO ignored_threads (mailbox_id, thread_id) VALUES ($1, $2) ON CONFLICT DO NOTHING", mailboxID, threadID)
	}
	w.WriteHeader(http.StatusOK)
}

func handleRecallMessage(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	messageID := parts[4]
	sender := r.URL.Query().Get("sender")
	if sender == "" {
		http.Error(w, "Sender required for authorization", http.StatusBadRequest)
		return
	}
	
	// Deep Logic: Verify the person recalling is the original sender
	res, _ := db.Exec("DELETE FROM messages WHERE id = $1 AND sender = $2 AND is_read = false", messageID, sender)
	rows, _ := res.RowsAffected()
	if rows > 0 {
		w.Write([]byte(`{"status":"recalled"}`))
	} else {
		w.Write([]byte(`{"status":"failed_or_read_or_unauthorized"}`))
	}
}

func handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	messageID := parts[4]
	
	var mailboxID string
	db.QueryRow("SELECT mailbox_id FROM messages WHERE id = $1", messageID).Scan(&mailboxID)
	
	HandleMessageDeletion(db, mailboxID, messageID)
	w.WriteHeader(http.StatusOK)
}
