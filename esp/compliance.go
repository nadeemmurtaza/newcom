package main

import (
	"database/sql"
	"log"
)

// HandleMessageDeletion intercepts a message deletion request and checks for Litigation Hold
func HandleMessageDeletion(db *sql.DB, mailboxID string, messageID string) error {
	var litigationHold bool
	err := db.QueryRow("SELECT litigation_hold FROM mailboxes WHERE id = $1", mailboxID).Scan(&litigationHold)
	if err != nil {
		return err
	}

	if litigationHold {
		log.Printf("Compliance: Mailbox %s is on Litigation Hold. Archiving message %s to Vault.", mailboxID, messageID)
		
		var orgID, rawBody string
		// Mock getting org_id and reconstructing raw body
		err = db.QueryRow(`
			SELECT m.org_id, msg.body_text 
			FROM mailboxes m 
			JOIN messages msg ON m.id = msg.mailbox_id 
			WHERE msg.id = $1`, messageID).Scan(&orgID, &rawBody)
		
		if err == nil {
			db.Exec("INSERT INTO vault_archives (org_id, original_message_id, raw_eml) VALUES ($1, $2, $3)", orgID, messageID, rawBody)
		} else {
			log.Printf("Compliance error fetching message details: %v", err)
		}
	}

	// Proceed with hard delete
	_, err = db.Exec("DELETE FROM messages WHERE id = $1 AND mailbox_id = $2", messageID, mailboxID)
	return err
}
