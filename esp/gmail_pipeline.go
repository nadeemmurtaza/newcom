package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"sync"

	"github.com/redis/go-redis/v9"
)

// Phase 27/55: Search Indexing Event Stream
//
// This file used to contain two more stages (startHeavyGuardWorker,
// startDeliveryWorker) that drained "email_inbound_queue" ->
// "email_clean_queue" and wrote messages themselves. Nothing anywhere ever
// pushed to "email_inbound_queue" -- the real inbound path is
// startInboundSMTPServer (inbound_smtp.go), which writes directly to
// Postgres. Those two stages were pure dead code, polling an eternally
// empty queue, and the delivery stage had a real bug besides
// (`SELECT id FROM mailboxes LIMIT 1` -- every message in that pipeline,
// had anything ever fed it, would have been misdelivered into whichever
// mailbox happened to be first in the whole table, any org).
//
// The one real, useful piece here -- the LSM-tree search indexer -- was
// only ever wired to that dead pipeline's "email_success_stream" event, so
// real mail (delivered via inbound_smtp.go) was never search-indexed at
// all. persistInboundMessage now publishes to "email_success_stream" after
// a successful insert, so this worker does real work on real mail.

// startSearchIndexerWorker subscribes to "email_success_stream" and indexes
// each newly stored message's text into the in-memory LSM-tree search index.
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
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				continue
			}
			if event["action"] != "NEW_MAIL" {
				continue
			}
			msgID := event["msg_id"]
			mailboxID := event["mailbox_id"]

			// messages carries the same tenant_isolation_policy RLS policy
			// (app.current_org_id) as everywhere else in this codebase; a
			// plain SELECT against it is always denied. Resolve the org via
			// mailboxes (no RLS there) and scope the read to a transaction
			// that sets it, same pattern as persistInboundMessage.
			var orgID string
			if err := db.QueryRow("SELECT org_id FROM mailboxes WHERE id = $1", mailboxID).Scan(&orgID); err != nil {
				log.Printf("LSM Indexer: failed to resolve org for mailbox %s: %v", mailboxID, err)
				continue
			}

			tx, err := db.Begin()
			if err != nil {
				log.Printf("LSM Indexer: failed to begin tx: %v", err)
				continue
			}
			if _, err := tx.Exec("SELECT set_config('app.current_org_id', $1, true)", orgID); err != nil {
				tx.Rollback()
				log.Printf("LSM Indexer: failed to set org context: %v", err)
				continue
			}

			var subject, bodyText, bodyHTML string
			err = tx.QueryRow("SELECT COALESCE(subject, ''), COALESCE(body_text, ''), COALESCE(body_html, '') FROM messages WHERE id = $1", msgID).Scan(&subject, &bodyText, &bodyHTML)
			tx.Commit()
			if err != nil {
				log.Printf("LSM Indexer: failed to load message %s: %v", msgID, err)
				continue
			}

			text := bodyText
			if text == "" {
				text = bodyHTML
			}
			lsm.IndexEmail(msgID, subject+" "+text)
			log.Printf("LSM Indexer: Indexed %s instantly to MemTable", msgID)
		}
	}
}
