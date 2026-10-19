package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Phase 45: A/B Split Testing & Multivariate Engine

func startABTestEvaluator(ctx context.Context, wg *sync.WaitGroup, db *sql.DB, rdb *redis.Client) {
	defer wg.Done()
	log.Println("A/B Test Engine: Statistical Evaluator Worker started")
	
	// Poll every 5 minutes in real life, every 10s for simulation
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			evaluateMatureABTests(db, rdb)
		}
	}
}

func evaluateMatureABTests(db *sql.DB, rdb *redis.Client) {
	// Find tests that have passed their evaluation window
	rows, err := db.Query(`
		SELECT id, campaign_id, org_id 
		FROM ab_tests 
		WHERE status = 'RUNNING' AND test_end_time <= NOW()
	`)
	if err != nil {
		log.Printf("[AB Engine] Error querying tests: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var testID, campaignID, orgID string
		if err := rows.Scan(&testID, &campaignID, &orgID); err != nil {
			continue
		}

		evaluateSingleTest(db, rdb, testID, campaignID, orgID)
	}
}

func evaluateSingleTest(db *sql.DB, rdb *redis.Client, testID, campaignID, orgID string) {
	log.Printf("[AB Engine] Evaluating test %s for Campaign %s", testID, campaignID)

	// Fetch variants
	vRows, err := db.Query(`
		SELECT id, variant_name, subject_line, sent_count, open_count 
		FROM ab_test_variants 
		WHERE ab_test_id = $1
	`, testID)
	
	if err != nil {
		return
	}
	defer vRows.Close()

	var winnerID string
	var winnerName, winnerSubject string
	var highestRate float64 = -1.0

	for vRows.Next() {
		var vID, vName, vSubject string
		var sent, opens int
		
		if err := vRows.Scan(&vID, &vName, &vSubject, &sent, &opens); err != nil { continue }

		var rate float64 = 0.0
		if sent > 0 {
			rate = float64(opens) / float64(sent)
		}

		log.Printf("[AB Engine] Variant %s: Sent=%d, Opens=%d, Rate=%.2f%%", vName, sent, opens, rate*100)

		if rate > highestRate {
			highestRate = rate
			winnerID = vID
			winnerName = vName
			winnerSubject = vSubject
		}
	}

	if winnerID != "" {
		log.Printf("[AB Engine] -> WINNER DECLARED: Variant %s (Subject: %s) with %.2f%% Open Rate", winnerName, winnerSubject, highestRate*100)

		// 1. Lock the winner in DB
		db.Exec("UPDATE ab_tests SET status = 'CONCLUDED', winner_variant_id = $1 WHERE id = $2", winnerID, testID)

		// 2. Dispatch the remainder of the audience with the winning variant.
		//
		// Phase 55: this used to be a log line and nothing else -- no job
		// ever reached email_outbound_queue. It now really enqueues one
		// real send per remaining contact, same path journey_engine.go
		// uses. Known limitation: this schema has no per-contact dispatch
		// log for the campaign, so "remainder" here means "everyone on the
		// list" rather than "the 80% who didn't get the initial test
		// split" -- getting the split itself exactly right needs a
		// campaign_contacts/sent-log table this project doesn't have yet.
		dispatchWinningVariant(db, rdb, campaignID, orgID, winnerSubject, winnerName)
	}
}

func dispatchWinningVariant(db *sql.DB, rdb *redis.Client, campaignID, orgID, subject, variantName string) {
	fromAddress := "campaigns@" + mailHostname()
	var verifiedDomain string
	if err := db.QueryRow("SELECT domain_name FROM domains WHERE org_id = $1 AND is_verified = true LIMIT 1", orgID).Scan(&verifiedDomain); err == nil {
		fromAddress = "campaigns@" + verifiedDomain
	}

	rows, err := db.Query(`
		SELECT c.email FROM contacts c
		JOIN campaigns camp ON c.list_id = camp.list_id
		WHERE camp.id = $1
	`, campaignID)
	if err != nil {
		log.Printf("[AB Engine] Failed to load recipients for campaign %s: %v", campaignID, err)
		return
	}
	defer rows.Close()

	dispatched := 0
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			continue
		}
		req := SendEmailRequest{
			MessageID: "abtest_" + campaignID + "_" + email,
			OrgID:     orgID,
			To:        email,
			From:      fromAddress,
			Subject:   subject,
		}
		reqJSON, _ := json.Marshal(req)
		rdb.LPush(context.Background(), "email_outbound_queue", reqJSON)
		dispatched++
	}

	db.Exec("UPDATE campaigns SET status = 'completed' WHERE id = $1", campaignID)
	log.Printf("[AB Engine] -> Dispatched winning variant %s to %d recipients for Campaign %s", variantName, dispatched, campaignID)
}
