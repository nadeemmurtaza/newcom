package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// StartScheduler begins background tasks for scheduled features
func StartScheduler(ctx context.Context, wg *sync.WaitGroup, db *sql.DB, rdb *redis.Client) {
	defer wg.Done()
	log.Println("Starting Scheduler Service...")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Scheduler: Shutting down gracefully...")
			return
		case <-ticker.C:
			// 1. Process Scheduled Outbound Emails from Redis ZSet
			processScheduledEmails(context.Background(), rdb)
		
			// 2. Process Snoozed Inbox Messages from Postgres
			processSnoozedMessages(context.Background(), db)
			
			// 3. Process Sweep Rules (Phase 16)
			processSweepRules(context.Background(), db)
		}
	}
}

func processSweepRules(ctx context.Context, db *sql.DB) {
	// Deep Logic: Database batching to prevent row-level locks on massive mailboxes
	batchSize := 1000

	// 1. Process 'delete_all' older than X days
	for {
		res, err := db.Exec(fmt.Sprintf(`
			DELETE FROM messages m
			WHERE id IN (
				SELECT m2.id FROM messages m2
				JOIN sweep_rules sr ON m2.mailbox_id = sr.mailbox_id AND m2.sender = sr.sender
				WHERE sr.action = 'delete_all'
				  AND m2.created_at < NOW() - (sr.days_older_than || ' days')::interval
				LIMIT %d
			)
		`, batchSize))
		
		if err != nil {
			log.Printf("Sweep error (delete_all): %v", err)
			break
		}
		rows, _ := res.RowsAffected()
		if rows == 0 { break }
		time.Sleep(100 * time.Millisecond) // Yield connection
	}

	// 2. Process 'keep_latest' (Delete all but the N latest)
	for {
		res, err := db.Exec(fmt.Sprintf(`
			WITH RankedMessages AS (
				SELECT m.id, ROW_NUMBER() OVER (PARTITION BY m.mailbox_id, m.sender ORDER BY m.created_at DESC) as rnk,
				       sr.days_older_than
				FROM messages m
				JOIN sweep_rules sr ON m.mailbox_id = sr.mailbox_id AND m.sender = sr.sender
				WHERE sr.action = 'keep_latest'
			)
			DELETE FROM messages 
			WHERE id IN (
				SELECT id FROM RankedMessages 
				WHERE rnk > days_older_than 
				LIMIT %d
			)
		`, batchSize))
		
		if err != nil {
			log.Printf("Sweep error (keep_latest): %v", err)
			break
		}
		rows, _ := res.RowsAffected()
		if rows == 0 { break }
		time.Sleep(100 * time.Millisecond)
	}

	// 3. Process 'move_to_folder' older than X days
	for {
		res, err := db.Exec(fmt.Sprintf(`
			UPDATE messages m
			SET folder_id = sub.target_folder_id
			FROM (
				SELECT m2.id, sr.target_folder_id 
				FROM messages m2
				JOIN sweep_rules sr ON m2.mailbox_id = sr.mailbox_id AND m2.sender = sr.sender
				WHERE sr.action = 'move_to_folder'
				  AND m2.created_at < NOW() - (sr.days_older_than || ' days')::interval
				  AND m2.folder_id != sr.target_folder_id
				LIMIT %d
			) sub
			WHERE m.id = sub.id
		`, batchSize))
		
		if err != nil {
			log.Printf("Sweep error (move_to_folder): %v", err)
			break
		}
		rows, _ := res.RowsAffected()
		if rows == 0 { break }
		time.Sleep(100 * time.Millisecond)
	}
}

func processScheduledEmails(ctx context.Context, rdb *redis.Client) {
	now := float64(time.Now().Unix())
	
	// Fetch emails due to be sent
	maxScore := fmt.Sprintf("%f", now)
	items, err := rdb.ZRangeByScore(ctx, "email_scheduled_queue", &redis.ZRangeBy{
		Min: "-inf",
		Max: maxScore,
	}).Result()
	
	if err != nil && err != redis.Nil {
		log.Printf("Scheduler error fetching from ZSet: %v", err)
		return
	}

	for _, item := range items {
		// Remove from scheduled queue
		removed, err := rdb.ZRem(ctx, "email_scheduled_queue", item).Result()
		if err != nil {
			continue
		}
		
		// If successfully removed, push to outbound queue
		if removed > 0 {
			err = rdb.LPush(ctx, "email_outbound_queue", item).Err()
			if err != nil {
				log.Printf("Scheduler failed to push to outbound queue: %v", err)
				// Put it back in scheduled queue to retry
				rdb.ZAdd(ctx, "email_scheduled_queue", redis.Z{Score: now, Member: item})
			}
		}
	}
}

func processSnoozedMessages(ctx context.Context, db *sql.DB) {
	// Unsnooze messages where snoozed_until has passed
	res, err := db.ExecContext(ctx, "UPDATE messages SET snoozed_until = NULL WHERE snoozed_until IS NOT NULL AND snoozed_until <= NOW()")
	if err != nil {
		log.Printf("Scheduler failed to unsnooze messages: %v", err)
		return
	}
	
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected > 0 {
		log.Printf("Scheduler unsnoozed %d messages", rowsAffected)
	}
}
