package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// Phase 47: Billing, Metering & Tier Quota Engine

type BillingEngine struct {
	db  *sql.DB
	rdb *redis.Client
}

func NewBillingEngine(db *sql.DB, rdb *redis.Client) *BillingEngine {
	return &BillingEngine{db: db, rdb: rdb}
}

// IsAccountActive checks if the organization is PAST_DUE
func (be *BillingEngine) IsAccountActive(orgID string) bool {
	var status string
	err := be.db.QueryRow("SELECT status FROM billing_subscriptions WHERE org_id = $1", orgID).Scan(&status)
	if err != nil {
		if err == sql.ErrNoRows {
			// No subscription found, default to FREE and ACTIVE
			be.db.Exec("INSERT INTO billing_subscriptions (org_id, tier, status) VALUES ($1, 'FREE', 'ACTIVE') ON CONFLICT DO NOTHING", orgID)
			return true
		}
		return false
	}
	return status == "ACTIVE"
}

// GetTierLimit maps string tiers to numerical limits
func (be *BillingEngine) GetTierLimit(orgID string) int64 {
	var tier string
	err := be.db.QueryRow("SELECT tier FROM billing_subscriptions WHERE org_id = $1", orgID).Scan(&tier)
	if err != nil {
		tier = "FREE" // Default
	}

	switch tier {
	case "FREE":
		return 10_000
	case "PRO":
		return 1_000_000
	case "ENTERPRISE":
		return 100_000_000
	default:
		return 10_000
	}
}

// CheckAndRecordUsage enforces quota mathematically in Redis
func (be *BillingEngine) CheckAndRecordUsage(ctx context.Context, orgID string) bool {
	limit := be.GetTierLimit(orgID)

	// YYYY-MM based quota
	monthStr := time.Now().UTC().Format("2006-01")
	key := fmt.Sprintf("billing:usage:%s:%s", orgID, monthStr)

	// Increment usage atomically
	currentUsage, err := be.rdb.Incr(ctx, key).Result()
	if err != nil {
		return false // Fail closed on DB error
	}
	
	if currentUsage == 1 {
		be.rdb.Expire(ctx, key, 60*24*time.Hour) // Keep for 2 months
	}

	if currentUsage > limit {
		// Undo the increment since we are blocking the send
		be.rdb.Decr(ctx, key)
		log.Printf("[Billing] Org %s hit quota limit (%d). Rejecting send.", orgID, limit)
		return false
	}

	return true
}

// ProcessStripeWebhook applies a verified Stripe webhook event (the caller,
// handleStripeWebhook in main.go, has already checked the Stripe-Signature
// header before this is called).
func (be *BillingEngine) ProcessStripeWebhook(eventType, orgID, invoiceID string, amount int) {
	log.Printf("[Billing] Received Stripe Webhook: %s for Org %s", eventType, orgID)

	switch eventType {
	case "invoice.payment_failed":
		// Mark subscription as PAST_DUE instantly cutting off API access
		be.db.Exec("UPDATE billing_subscriptions SET status = 'PAST_DUE', updated_at = NOW() WHERE org_id = $1", orgID)
		
		be.db.Exec(`
			INSERT INTO billing_invoices (org_id, stripe_invoice_id, amount_due, status) 
			VALUES ($1, $2, $3, 'OPEN')
			ON CONFLICT (stripe_invoice_id) DO UPDATE SET status = 'OPEN', amount_due = $3
		`, orgID, invoiceID, amount)
		
		log.Printf("[Billing] Auto-Suspended Outbound API for Org %s due to failed payment.", orgID)

	case "invoice.paid":
		// Restore access
		be.db.Exec("UPDATE billing_subscriptions SET status = 'ACTIVE', updated_at = NOW() WHERE org_id = $1", orgID)
		be.db.Exec("UPDATE billing_invoices SET status = 'PAID' WHERE stripe_invoice_id = $1", invoiceID)
		log.Printf("[Billing] Restored Outbound API for Org %s.", orgID)
	}
}
