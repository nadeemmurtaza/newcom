package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Phase 29: Dynamic Outbound MTA Pool & IP Warmup

type IPTier string

const (
	TierPremium    IPTier = "PREMIUM"
	TierWarmup     IPTier = "WARMUP"
	TierQuarantine IPTier = "QUARANTINE"
)

// IPNode represents a single outbound IP address in the Google MTA pool
type IPNode struct {
	IPAddress    string
	Tier         IPTier
	Bounces      int
	TargetCounts map[string]int // Destination (e.g. outlook.com) -> Sent Count
	PausedUntil  map[string]time.Time // Destination -> Backoff Time
	Breaker      *CircuitBreaker // Phase 33: Reliability Hardening
	mu           sync.Mutex
}

func (ip *IPNode) CanSendTo(destination string, isPremium bool) bool {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	// Check Dynamic Back-off (SMTP 421 Pause)
	if pauseTime, exists := ip.PausedUntil[destination]; exists {
		if time.Now().Before(pauseTime) {
			return false // Still in back-off
		}
	}

	// Traffic Sharding Limits
	if ip.Tier == TierWarmup && !isPremium {
		// New domains have a strict limit per destination per day
		if ip.TargetCounts[destination] >= 100 {
			return false // Need spillover
		}
	}
	return true
}

func (ip *IPNode) RecordSend(destination string) {
	ip.mu.Lock()
	defer ip.mu.Unlock()
	ip.TargetCounts[destination]++
}

func (ip *IPNode) ApplyBackoff(destination string) {
	ip.mu.Lock()
	defer ip.mu.Unlock()
	// Pause this IP for this destination for 1 hour
	ip.PausedUntil[destination] = time.Now().Add(1 * time.Hour)
	log.Printf("[Destination Rate Shaper] IP %s paused for %s due to SMTP 421 Rate Limit", ip.IPAddress, destination)
}

type OutboundRouter struct {
	IPs []*IPNode
}

// NewOutboundRouter builds the outbound IP pool from OUTBOUND_IP_POOL (a
// comma-separated list of IPs this host actually owns and can bind to,
// ideally with their own PTR/rDNS set up for deliverability).
//
// Phase 53: this used to default to a handful of hardcoded addresses in
// Google's own public ranges -- IPs this deployment doesn't own and can't
// bind a local socket to, so PerformHandshake would fail "invalid source
// IP" on every real send. With nothing configured, the pool is a single
// entry with an empty IPAddress, which tells PerformHandshake to let the OS
// pick the default route instead of binding a specific (and fake) source.
func NewOutboundRouter() *OutboundRouter {
	var ips []string
	if pool := strings.TrimSpace(os.Getenv("OUTBOUND_IP_POOL")); pool != "" {
		for _, ip := range strings.Split(pool, ",") {
			if ip = strings.TrimSpace(ip); ip != "" {
				ips = append(ips, ip)
			}
		}
	}
	if len(ips) == 0 {
		ips = []string{""}
	}

	router := &OutboundRouter{}
	for _, ip := range ips {
		router.IPs = append(router.IPs, &IPNode{
			IPAddress:    ip,
			Tier:         TierWarmup,
			TargetCounts: make(map[string]int),
			PausedUntil:  make(map[string]time.Time),
			Breaker:      NewCircuitBreaker(3, 30*time.Second),
		})
	}
	return router
}

// getIPForDelivery picks the first pool IP with capacity for destination.
//
// Phase 53: this used to gate on IPNode.Tier (TierPremium vs TierWarmup) to
// decide which IPs an org could use, but the pool is now whatever the
// operator actually configured via OUTBOUND_IP_POOL -- there's no basis for
// treating any of them as more "premium" than another. An org with its own
// dedicated IP is already routed to it earlier via GetDedicatedIPForDispatch;
// everyone else shares the configured pool.
func (router *OutboundRouter) getIPForDelivery(destination string, isPremiumOrg bool) *IPNode {
	for _, ip := range router.IPs {
		if ip.CanSendTo(destination, isPremiumOrg) {
			return ip
		}
	}
	return nil
}

// 4. Automated Reputation Repair
func (router *OutboundRouter) startReputationRepairWorker(ctx context.Context) {
	log.Println("Outbound Router: Automated Reputation Repair Cooldown worker started")
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
			for _, ip := range router.IPs {
				ip.mu.Lock()
				// Quarantine logic
				if ip.Tier == TierPremium && ip.Bounces > 50 {
					log.Printf("[Reputation Repair] DOWNGRADE: Premium IP %s blacklisted! Moving to QUARANTINE.", ip.IPAddress)
					ip.Tier = TierQuarantine
				}
				// Ramp-up logic
				if ip.Tier == TierQuarantine {
					ip.Bounces -= 10 // Slowly forget bounces
					if ip.Bounces <= 0 {
						ip.Bounces = 0
						ip.Tier = TierWarmup
						log.Printf("[Reputation Repair] UPGRADE: Quarantined IP %s is clean. Moving to WARMUP.", ip.IPAddress)
					}
				}
				ip.mu.Unlock()
			}
		}
	}
}

// Main Outbound Worker intercepting the Java Spring Batch queue
func startOutboundMTAWorker(ctx context.Context, wg *sync.WaitGroup, db *sql.DB, rdb *redis.Client, router *OutboundRouter, riskEngine *IdentityRiskEngine) {
	defer wg.Done()
	log.Println("Outbound Router: MTA Delivery Pool started")

	go router.startReputationRepairWorker(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Pop emails pushed by the Java Campaign Engine
			res, err := rdb.BRPop(ctx, 2*time.Second, "email_outbound_queue").Result()
			if err != nil {
				continue
			}

			payload := res[1]
			var req map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &req); err != nil {
				RouteToDLQ(ctx, rdb, "email_outbound_queue", payload, "Invalid JSON format from Campaign Engine")
				continue
			}

			rcpt, ok := req["To"].(string)
			if !ok { continue }

			// In a real system, we parse the true Sender ID. For simulation, use a fake UUID.
			accountID := "00000000-0000-0000-0000-000000000000"
			body, _ := req["Body"].(string)

			// Phase 30: Outbound Telemetry Hooks
			if !riskEngine.EvaluateOutboundVelocity(accountID) {
				continue // Dropped by Kill Switch
			}
			if !riskEngine.ScanOutboundContent(accountID, body) {
				continue // Dropped by Kill Switch
			}

			// Phase 48: Advanced Anti-Abuse (Machine Learning Hook)
			orgID, _ := req["OrgID"].(string)
			subject, _ := req["Subject"].(string)
			mlScore := globalMLScanner.ScanContent(orgID, subject, body)
			if mlScore > 0.85 {
				log.Printf("[ML Anti-Abuse] High Phishing Probability (%.2f). Blocking message.", mlScore)
				// Hard DLQ route to preserve forensic evidence
				RouteToDLQ(ctx, rdb, "email_outbound_queue", payload, "ML_PHISHING_BLOCKED")
				
				// In a real system, trigger a PostgreSQL high-severity audit here:
				// globalRulesEngine.LogAudit(orgID, accountID, "ML_PHISHING_BLOCKED", "SYSTEM", "")
				continue
			}

			parts := strings.Split(rcpt, "@")
			if len(parts) != 2 { continue }
			destination := parts[1] // e.g. outlook.com

			// Phase 40: Hard-Stop Suppression List Enforcement
			var suppressedID string
			err = db.QueryRow("SELECT id FROM suppression_list WHERE email = $1 AND org_id = $2", rcpt, orgID).Scan(&suppressedID)
			if err == nil {
				log.Printf("[Dispatch] Recipient %s is globally suppressed. Dropping message.", rcpt)
				continue
			}

			// Phase 46: Dedicated IP Routing & Spillover
			var dedicatedIP string
			if orgID != "" {
				dedicatedIP = GetDedicatedIPForDispatch(ctx, db, rdb, orgID)
			}
			
			var ipNode *IPNode
			if dedicatedIP != "" {
				ipNode = &IPNode{
					IPAddress: dedicatedIP,
					Tier:      "DEDICATED",
					Breaker:   NewCircuitBreaker(3, 10*time.Second), // In reality, we'd cache these
				}
			} else {
				// Simulate determining if the sender organization is an established enterprise
				isPremiumOrg := rand.Float32() > 0.8 // 20% are premium
				ipNode = router.getIPForDelivery(destination, isPremiumOrg)
			}
			if ipNode == nil {
				log.Printf("Outbound Router: NO ROUTES AVAILABLE for %s. Deferring email.", destination)
				// Requeue for later
				rdb.LPush(ctx, "email_outbound_queue", payload)
				time.Sleep(1 * time.Second)
				continue
			}

			// Phase 40: Provider-Aware Throttling
			if !router.WaitThrottle(ctx, rdb, destination) {
				log.Printf("[Throttler] %s limit saturated. Pushing to delayed queue.", destination)
				requeueExponential(ctx, rdb, req)
				continue
			}

			// Phase 53: Real delivery. Build and DKIM-sign the message, then
			// actually dial the recipient's MX and speak SMTP -- this used
			// to be entirely rand.Float32()-simulated and never touched the
			// network at all (PerformHandshake/MTATrafficShaper existed but
			// nothing ever called them).
			from, _ := req["From"].(string)
			subjectStr, _ := req["Subject"].(string)
			msgIDStr, _ := req["MessageID"].(string)
			raw := buildRawMessage(from, rcpt, subjectStr, body, msgIDStr)
			raw = signOutboundMessage(db, raw, from)

			// Phase 33: Reliability Hardening (Circuit Breaker)
			var statusCode int
			err = ipNode.Breaker.Execute(func() error {
				statusCode, err = globalMTAShaper.PerformHandshake(&SMTPSession{
					IPAddress:    ipNode.IPAddress,
					TargetDomain: destination,
					Recipient:    rcpt,
					Payload:      raw,
				})

				switch {
				case statusCode >= 200 && statusCode < 300:
					// Success
					ipNode.RecordSend(destination)
					globalPostmaster.RecordMetric(ctx, from, "sent", 1)
					if orgID != "" {
						QueueWebhookEvent(rdb, WebhookEvent{
							OrgID:     orgID,
							MessageID: msgIDStr,
							EventType: "delivered",
							Metadata:  `{"email": "` + rcpt + `", "ip": "` + ipNode.IPAddress + `"}`,
						})
					}
					return nil

				case statusCode == 421 || statusCode == 450 || statusCode == 451 || statusCode == 452:
					// Soft bounce / rate limited -- not a circuit-breaker trip
					ipNode.ApplyBackoff(destination)
					return nil

				case statusCode >= 500 && statusCode < 600:
					// Hard bounce
					ipNode.mu.Lock()
					ipNode.Bounces += 5
					ipNode.mu.Unlock()
					db.Exec("INSERT INTO suppression_list (org_id, email, reason) VALUES ($1, $2, 'HARD_BOUNCE') ON CONFLICT DO NOTHING", orgID, rcpt)
					globalPostmaster.RecordMetric(ctx, from, "bounced", 1)
					return nil

				default:
					// Connection-level failure (0 or unparseable) -- trip the breaker
					return fmt.Errorf("delivery failed with status %d: %v", statusCode, err)
				}
			})

			if err != nil {
				log.Printf("Delivery aborted by Circuit Breaker for %s: %v", ipNode.IPAddress, err)
				requeueExponential(ctx, rdb, req)
			} else if statusCode == 421 || statusCode == 450 || statusCode == 451 || statusCode == 452 {
				log.Printf("[SMTP %d] Soft bounce from %s. Applying exponential backoff.", statusCode, destination)
				requeueExponential(ctx, rdb, req)
			} else {
				log.Printf("Outbound Router: Processed email to %s via %s IP %s (status %d)", destination, ipNode.Tier, ipNode.IPAddress, statusCode)
			}
		}
	}
}

// Phase 40: Provider-Aware Throttling
func (router *OutboundRouter) WaitThrottle(ctx context.Context, rdb *redis.Client, destination string) bool {
	key := "throttle:" + destination
	count, _ := rdb.Incr(ctx, key).Result()
	if count == 1 {
		rdb.Expire(ctx, key, 10*time.Second) // 10 second window
	}
	
	// Different limits for different providers (simulated)
	limit := 50
	if destination == "yahoo.com" { limit = 20 }
	if destination == "gmail.com" { limit = 100 }
	
	return count <= int64(limit)
}

// Phase 40: Smart Exponential Backoff
func requeueExponential(ctx context.Context, rdb *redis.Client, req map[string]interface{}) {
	attemptsFloat, _ := req["retry_count"].(float64)
	attempts := int(attemptsFloat)
	attempts++
	
	if attempts > 5 {
		log.Printf("[Dispatch] Hard failure for %v after 5 retries. Dropping.", req["To"])
		return
	}
	
	req["retry_count"] = attempts
	reqJSON, _ := json.Marshal(req)
	
	// Delay: 2^attempts * 60 seconds (2m, 4m, 8m, 16m...)
	// For simulation, we'll use seconds (2s, 4s, 8s)
	delaySecs := (1 << attempts) * 2 
	score := time.Now().Unix() + int64(delaySecs)
	
	rdb.ZAdd(ctx, "email_delayed_queue", redis.Z{Score: float64(score), Member: string(reqJSON)})
}

// Phase 40: Delayed Queue Poller
func startDelayedQueueWorker(ctx context.Context, wg *sync.WaitGroup, rdb *redis.Client) {
	defer wg.Done()
	log.Println("Outbound Router: Delayed Queue Worker started")
	
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := fmt.Sprintf("%d", time.Now().Unix())
			
			// Get mature items
			items, err := rdb.ZRangeByScore(ctx, "email_delayed_queue", &redis.ZRangeBy{
				Min: "-inf",
				Max: now,
			}).Result()
			
			if err != nil || len(items) == 0 {
				continue
			}
			
			// Remove from delayed queue and push to active queue
			for _, item := range items {
				rdb.ZRem(ctx, "email_delayed_queue", item)
				rdb.LPush(ctx, "email_outbound_queue", item)
				log.Printf("[Delayed Queue] Re-injecting mature payload into active queue")
			}
		}
	}
}
