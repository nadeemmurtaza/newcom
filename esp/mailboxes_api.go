package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Phase 51: Unlimited Mailbox Provisioning per Domain

var localPartPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,63})?$`)

var defaultSystemFolders = []string{"INBOX", "SENT", "DRAFTS", "SPAM", "TRASH", "ARCHIVE"}

type MailboxSummary struct {
	ID           string `json:"id"`
	EmailAddress string `json:"email_address"`
	IsCatchAll   bool   `json:"is_catch_all"`
	CreatedAt    string `json:"created_at"`
}

// handleCreateMailbox provisions a new mailbox under a domain. No cap on count:
// an org may create as many mailboxes on a domain as it wants.
// POST /api/domains/{id}/mailboxes  body: {"local_part": "jane", "is_catch_all": false}
func handleCreateMailbox(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	if orgID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	domainID := r.PathValue("id")

	var domainName string
	err := db.QueryRow("SELECT domain_name FROM domains WHERE id = $1 AND org_id = $2", domainID, orgID).Scan(&domainName)
	if err != nil {
		http.Error(w, "Domain not found", http.StatusNotFound)
		return
	}

	var req struct {
		LocalPart  string `json:"local_part"`
		IsCatchAll bool   `json:"is_catch_all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	localPart := strings.ToLower(strings.TrimSpace(req.LocalPart))
	if !localPartPattern.MatchString(localPart) {
		http.Error(w, "Invalid local_part", http.StatusBadRequest)
		return
	}
	emailAddress := localPart + "@" + domainName

	var mailboxID string
	err = db.QueryRow(`
		INSERT INTO mailboxes (org_id, email_address, is_catch_all)
		VALUES ($1, $2, $3)
		RETURNING id
	`, orgID, emailAddress, req.IsCatchAll).Scan(&mailboxID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			http.Error(w, "Mailbox already exists", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create mailbox: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, name := range defaultSystemFolders {
		if _, err := db.Exec(`
			INSERT INTO folders (mailbox_id, name, is_system) VALUES ($1, $2, true)
			ON CONFLICT (mailbox_id, name) DO NOTHING
		`, mailboxID, name); err != nil {
			http.Error(w, "Failed to provision folders: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Issue one initial app password so the mailbox is usable immediately.
	rawToken := make([]byte, 20)
	if _, err := rand.Read(rawToken); err != nil {
		http.Error(w, "Failed to generate credentials", http.StatusInternalServerError)
		return
	}
	plainPassword := hex.EncodeToString(rawToken)
	tokenHash, err := bcrypt.GenerateFromPassword([]byte(plainPassword), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Failed to secure credentials", http.StatusInternalServerError)
		return
	}
	if _, err := db.Exec(`
		INSERT INTO app_passwords (mailbox_id, token_hash, device_name) VALUES ($1, $2, 'initial')
	`, mailboxID, string(tokenHash)); err != nil {
		http.Error(w, "Failed to store credentials", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":            mailboxID,
		"email_address": emailAddress,
		"is_catch_all":  req.IsCatchAll,
		"app_password":  plainPassword, // shown once; only the bcrypt hash is stored
	})
}

// handleListMailboxes lists every mailbox provisioned on a domain.
// GET /api/domains/{id}/mailboxes
func handleListMailboxes(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	if orgID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	domainID := r.PathValue("id")

	var domainName string
	if err := db.QueryRow("SELECT domain_name FROM domains WHERE id = $1 AND org_id = $2", domainID, orgID).Scan(&domainName); err != nil {
		http.Error(w, "Domain not found", http.StatusNotFound)
		return
	}

	rows, err := db.Query(`
		SELECT id, email_address, is_catch_all, created_at FROM mailboxes
		WHERE org_id = $1 AND email_address LIKE '%@' || $2
		ORDER BY created_at DESC
	`, orgID, domainName)
	if err != nil {
		http.Error(w, "Failed to query mailboxes", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var out []MailboxSummary
	for rows.Next() {
		var m MailboxSummary
		if err := rows.Scan(&m.ID, &m.EmailAddress, &m.IsCatchAll, &m.CreatedAt); err == nil {
			out = append(out, m)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// handleDeleteMailbox removes a mailbox. Messages/folders/app_passwords cascade via FK.
// DELETE /api/mailboxes/{id}
func handleDeleteMailbox(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	if orgID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	mailboxID := r.PathValue("id")

	res, err := db.Exec("DELETE FROM mailboxes WHERE id = $1 AND org_id = $2", mailboxID, orgID)
	if err != nil {
		http.Error(w, "Failed to delete mailbox", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "Mailbox not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
