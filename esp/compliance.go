package main

import (
	"database/sql"
	"log"
)

// HandleMessageDeletion intercepts a message deletion request and checks for Litigation Hold.
//
// Phase 52: this previously ran everything as plain db.QueryRow/db.Exec
// calls against messages, which the tenant_isolation_policy RLS policy
// (app.current_org_id) always denies -- the hard-delete at the end could
// never actually delete anything. Now resolves the mailbox's org (mailboxes
// has no RLS) and does the vault-archive + delete inside one org-scoped
// transaction.
func HandleMessageDeletion(db *sql.DB, mailboxID string, messageID string) error {
	var litigationHold bool
	var orgID string
	err := db.QueryRow("SELECT litigation_hold, org_id FROM mailboxes WHERE id = $1", mailboxID).Scan(&litigationHold, &orgID)
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

	if litigationHold {
		log.Printf("Compliance: Mailbox %s is on Litigation Hold. Archiving message %s to Vault.", mailboxID, messageID)

		var rawBody string
		err = tx.QueryRow("SELECT COALESCE(body_text, '') FROM messages WHERE id = $1", messageID).Scan(&rawBody)
		if err == nil {
			if _, err := tx.Exec("INSERT INTO vault_archives (org_id, original_message_id, raw_eml) VALUES ($1, $2, $3)", orgID, messageID, rawBody); err != nil {
				return err
			}
		} else {
			log.Printf("Compliance error fetching message details: %v", err)
		}
	}

	// Proceed with hard delete
	if _, err := tx.Exec("DELETE FROM messages WHERE id = $1 AND mailbox_id = $2", messageID, mailboxID); err != nil {
		return err
	}
	return tx.Commit()
}
