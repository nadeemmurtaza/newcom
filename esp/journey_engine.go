package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Phase 44: Automation & Journey Builder (DAG Engine)

type JourneyEngine struct {
	db  *sql.DB
	rdb *redis.Client
}

func NewJourneyEngine(db *sql.DB, rdb *redis.Client) *JourneyEngine {
	return &JourneyEngine{db: db, rdb: rdb}
}

// TriggerJourney injects a user into the starting node of a journey
func (je *JourneyEngine) TriggerJourney(orgID, journeyID, email string) error {
	// Find the TRIGGER node for this journey
	var startNodeID string
	err := je.db.QueryRow(`
		SELECT id FROM journey_nodes 
		WHERE journey_id = $1 AND node_type = 'TRIGGER'
	`, journeyID).Scan(&startNodeID)
	if err != nil {
		return fmt.Errorf("could not find starting TRIGGER node for journey: %v", err)
	}

	// Insert contact (ignore if already exists to prevent duplicate execution)
	_, err = je.db.Exec(`
		INSERT INTO journey_contacts (journey_id, email, current_node_id, status, next_execute_at)
		VALUES ($1, $2, $3, 'IN_PROGRESS', NOW())
		ON CONFLICT (journey_id, email) DO NOTHING
	`, journeyID, email, startNodeID)
	
	return err
}

func startJourneyWorker(ctx context.Context, wg *sync.WaitGroup, db *sql.DB, rdb *redis.Client) {
	defer wg.Done()
	log.Println("Automation Engine: Journey Builder DAG Worker started")
	
	// Poll every 5 seconds for matured nodes
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processMatureJourneyContacts(db, rdb)
		}
	}
}

type JourneyNode struct {
	ID              string
	NodeType        string
	Config          map[string]interface{}
	NextNodeID      sql.NullString
	FalseNextNodeID sql.NullString
}

func processMatureJourneyContacts(db *sql.DB, rdb *redis.Client) {
	// Fetch a batch of 100 contacts ready to advance
	rows, err := db.Query(`
		SELECT jc.id, jc.email, jn.id, jn.node_type, jn.config, jn.next_node_id, jn.false_next_node_id, j.org_id
		FROM journey_contacts jc
		JOIN journey_nodes jn ON jc.current_node_id = jn.id
		JOIN journeys j ON jc.journey_id = j.id
		WHERE jc.status = 'IN_PROGRESS' 
		  AND jc.next_execute_at <= NOW()
		  AND j.status = 'ACTIVE'
		LIMIT 100
		FOR UPDATE SKIP LOCKED
	`)
	
	if err != nil {
		log.Printf("[Journey Engine] Failed to fetch contacts: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var contactID, email, nodeID, nodeType, orgID string
		var configBytes []byte
		var nextNodeID, falseNextNodeID sql.NullString
		
		err := rows.Scan(&contactID, &email, &nodeID, &nodeType, &configBytes, &nextNodeID, &falseNextNodeID, &orgID)
		if err != nil { continue }

		var config map[string]interface{}
		json.Unmarshal(configBytes, &config)
		
		node := JourneyNode{
			ID:              nodeID,
			NodeType:        nodeType,
			Config:          config,
			NextNodeID:      nextNodeID,
			FalseNextNodeID: falseNextNodeID,
		}

		executeDAGNode(db, rdb, contactID, email, orgID, node)
	}
}

func executeDAGNode(db *sql.DB, rdb *redis.Client, contactID, email, orgID string, node JourneyNode) {
	var nextID string
	var nextExecAt time.Time = time.Now() // Default to immediate execution for the next node
	
	switch node.NodeType {
	case "TRIGGER":
		// Standard passthrough
		if node.NextNodeID.Valid { nextID = node.NextNodeID.String }
		
	case "DELAY":
		// Calculate delay
		if delayHours, ok := node.Config["delay_hours"].(float64); ok {
			nextExecAt = time.Now().Add(time.Duration(delayHours) * time.Hour)
		} else if delayMins, ok := node.Config["delay_minutes"].(float64); ok {
			nextExecAt = time.Now().Add(time.Duration(delayMins) * time.Minute)
		}
		if node.NextNodeID.Valid { nextID = node.NextNodeID.String }
		
	case "ACTION_SEND_EMAIL":
		// Phase 55: resolve a real verified domain this org owns instead of
		// the nonsense "automation@<uuid>.com" placeholder -- that domain
		// never existed, so DKIM signing (outbound_dkim.go) would find no
		// key for it and every journey email went out unsigned from an
		// address nobody could reply to.
		fromAddress := "automation@" + mailHostname()
		var verifiedDomain string
		if err := db.QueryRow("SELECT domain_name FROM domains WHERE org_id = $1 AND is_verified = true LIMIT 1", orgID).Scan(&verifiedDomain); err == nil {
			fromAddress = "automation@" + verifiedDomain
		}

		msgID := "journey_" + contactID + "_" + fmt.Sprintf("%d", time.Now().UnixNano())
		req := SendEmailRequest{
			MessageID: msgID,
			OrgID:     orgID,
			To:        email,
			From:      fromAddress,
		}

		if subj, ok := node.Config["subject"].(string); ok { req.Subject = subj }
		if body, ok := node.Config["body"].(string); ok { req.Body = body }
		if tplID, ok := node.Config["template_id"].(string); ok { req.TemplateID = tplID }

		reqJSON, _ := json.Marshal(req)
		rdb.LPush(context.Background(), "email_outbound_queue", reqJSON)

		// Record which message this contact's next CONDITION_OPENED node
		// (if any) should check for an open event.
		db.Exec("UPDATE journey_contacts SET last_sent_message_id = $1 WHERE id = $2", msgID, contactID)

		log.Printf("[Journey Engine] Executed ACTION_SEND_EMAIL for %s", email)
		if node.NextNodeID.Valid { nextID = node.NextNodeID.String }

	case "CONDITION_OPENED":
		// Phase 55: this used to hardcode opened := false and never check
		// anything -- every CONDITION_OPENED branch always took the "not
		// opened" path, regardless of whether the recipient ever opened the
		// email. handleTrackOpen (tracking.go) logs a real 'open' row in
		// events keyed by message_id when the tracking pixel fires; check
		// that instead.
		var lastMessageID sql.NullString
		db.QueryRow("SELECT last_sent_message_id FROM journey_contacts WHERE id = $1", contactID).Scan(&lastMessageID)

		opened := false
		if lastMessageID.Valid {
			db.QueryRow(
				"SELECT EXISTS(SELECT 1 FROM events WHERE message_id = $1 AND event_type = 'open')",
				lastMessageID.String,
			).Scan(&opened)
		}

		if opened {
			if node.NextNodeID.Valid { nextID = node.NextNodeID.String }
		} else {
			if node.FalseNextNodeID.Valid { nextID = node.FalseNextNodeID.String }
		}
		log.Printf("[Journey Engine] Evaluated CONDITION_OPENED for %s (Result: %v)", email, opened)
	}

	// Update the contact's state
	if nextID == "" {
		// End of Journey
		db.Exec("UPDATE journey_contacts SET status = 'COMPLETED', current_node_id = NULL WHERE id = $1", contactID)
		log.Printf("[Journey Engine] Contact %s completed the journey.", email)
	} else {
		db.Exec("UPDATE journey_contacts SET current_node_id = $1, next_execute_at = $2 WHERE id = $3", nextID, nextExecAt, contactID)
	}
}
