package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

// Phase 39: Enterprise Mailbox System

type MailboxManager struct {
	db  *sql.DB
	rdb *redis.Client
}

func NewMailboxManager(db *sql.DB, rdb *redis.Client) *MailboxManager {
	return &MailboxManager{db: db, rdb: rdb}
}

// 1. Alias & Catch-All Resolution
func (mm *MailboxManager) ResolveRecipient(domainID string, incomingEmail string) (string, error) {
	var mailboxID string
	
	// A. Check for exact match in mailboxes
	err := mm.db.QueryRow(`
		SELECT id FROM mailboxes 
		WHERE email_address = $1 AND org_id = (SELECT org_id FROM domains WHERE id = $2)
	`, incomingEmail, domainID).Scan(&mailboxID)
	
	if err == nil {
		return mailboxID, nil
	}

	// B. Check for Alias match
	err = mm.db.QueryRow(`
		SELECT m.id FROM email_aliases a
		JOIN mailboxes m ON a.mailbox_id = m.id
		WHERE a.alias_address = $1 AND m.org_id = (SELECT org_id FROM domains WHERE id = $2)
	`, incomingEmail, domainID).Scan(&mailboxID)

	if err == nil {
		return mailboxID, nil
	}

	// C. Check for Catch-All
	err = mm.db.QueryRow(`
		SELECT id FROM mailboxes 
		WHERE is_catch_all = true AND org_id = (SELECT org_id FROM domains WHERE id = $1)
		LIMIT 1
	`, domainID).Scan(&mailboxID)

	if err == nil {
		return mailboxID, nil
	}

	return "", fmt.Errorf("Mailbox not found")
}

// 2. Velocity Limiter (Per-Mailbox)
func (mm *MailboxManager) CheckVelocityLimit(ctx context.Context, mailboxID string) bool {
	// First fetch the mailbox limit
	var limit int
	err := mm.db.QueryRow("SELECT daily_send_limit FROM mailboxes WHERE id = $1", mailboxID).Scan(&limit)
	if err != nil {
		limit = 2000 // default fallback
	}

	// Redis atomic counter: mailbox_send:<id>:<YYYY-MM-DD>
	key := fmt.Sprintf("mailbox_send:%s:%s", mailboxID, time.Now().Format("2006-01-02"))
	count, err := mm.rdb.Incr(ctx, key).Result()
	if err != nil {
		log.Printf("MailboxManager: Redis error on quota check %v", err)
		return true // Fail open
	}

	if count == 1 {
		mm.rdb.Expire(ctx, key, 48*time.Hour)
	}

	if int(count) > limit {
		log.Printf("ALERT: Mailbox %s exceeded daily limit of %d", mailboxID, limit)
		// Option: Suspend mailbox automatically here
		return false
	}
	
	return true
}

// 3. App Password Verification
//
// Phase 55: this queried "WHERE token_hash = $1" with the raw plaintext
// token -- token_hash stores a bcrypt hash (see mailboxes_api.go,
// mailbox_auth.go), which can never equal its own plaintext input. This
// could not have matched a real app password, ever; it was also unused
// anywhere in the codebase. Fixed to actually bcrypt-compare, same
// approach handleMailboxLogin (mailbox_auth.go) uses -- since app
// passwords aren't looked up by email here, this has to scan every row.
func (mm *MailboxManager) VerifyAppPassword(token string) (string, error) {
	rows, err := mm.db.Query("SELECT id, mailbox_id, token_hash FROM app_passwords")
	if err != nil {
		return "", fmt.Errorf("Invalid App Password")
	}
	defer rows.Close()

	for rows.Next() {
		var id, mailboxID, hash string
		if err := rows.Scan(&id, &mailboxID, &hash); err != nil {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(token)) == nil {
			mm.db.Exec("UPDATE app_passwords SET last_used_at = NOW() WHERE id = $1", id)
			return mailboxID, nil
		}
	}

	return "", fmt.Errorf("Invalid App Password")
}
// Phase 42: Mailbox Initialization (System Folders)
func (mm *MailboxManager) InitializeMailbox(orgID, emailAddress string) (string, error) {
	var mailboxID string
	err := mm.db.QueryRow(`
		INSERT INTO mailboxes (org_id, email_address) 
		VALUES ($1, $2) RETURNING id
	`, orgID, emailAddress).Scan(&mailboxID)
	
	if err != nil {
		return "", err
	}

	systemFolders := []string{"INBOX", "SENT", "DRAFTS", "OUTBOX", "SPAM", "TRASH", "ARCHIVE"}
	for _, folder := range systemFolders {
		_, err := mm.db.Exec(`
			INSERT INTO folders (mailbox_id, name, is_system) 
			VALUES ($1, $2, true)
		`, mailboxID, folder)
		if err != nil {
			log.Printf("Failed to create system folder %s for mailbox %s: %v", folder, mailboxID, err)
		}
	}
	
	return mailboxID, nil
}

// Phase 42: Inbound Spam Classification
func (mm *MailboxManager) ClassifyInbound(mailboxID, subject, body string) string {
	// A real ESP would run Bayesian filtering and check DKIM/SPF headers here.
	// We'll simulate a basic keyword heuristic.
	spamKeywords := []string{"viagra", "lottery", "prince", "crypto", "urgent wire"}
	isSpam := false
	lowerBody := strings.ToLower(body)
	lowerSubject := strings.ToLower(subject)
	
	for _, kw := range spamKeywords {
		if strings.Contains(lowerBody, kw) || strings.Contains(lowerSubject, kw) {
			isSpam = true
			break
		}
	}

	targetFolder := "INBOX"
	if isSpam {
		targetFolder = "SPAM"
	}

	var folderID string
	err := mm.db.QueryRow("SELECT id FROM folders WHERE mailbox_id = $1 AND name = $2", mailboxID, targetFolder).Scan(&folderID)
	if err != nil {
		log.Printf("Failed to resolve folder %s for mailbox %s", targetFolder, mailboxID)
		return ""
	}
	
	return folderID
}

// Phase 42: Auto-Expunge Lifecycle (30-Day Retention)
func startAutoExpungeWorker(ctx context.Context, wg *sync.WaitGroup, db *sql.DB) {
	defer wg.Done()
	log.Println("Lifecycle Manager: Auto-Expunge Worker started")

	// Run once an hour in real life; simulate ticker
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Run immediately on boot for simulation
	runAutoExpunge(db)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runAutoExpunge(db)
		}
	}
}

func runAutoExpunge(db *sql.DB) {
	// Find messages in TRASH or SPAM older than 30 days
	// Using PostgreSQL syntax for date math
	res, err := db.Exec(`
		DELETE FROM messages 
		WHERE folder_id IN (
			SELECT id FROM folders WHERE name IN ('TRASH', 'SPAM')
		)
		AND created_at < NOW() - INTERVAL '30 days'
	`)
	
	if err == nil {
		count, _ := res.RowsAffected()
		if count > 0 {
			log.Printf("[Auto-Expunge] Automatically deleted %d expired messages from Trash/Spam", count)
		}
	} else {
		log.Printf("[Auto-Expunge] Failed to sweep database: %v", err)
	}
}
