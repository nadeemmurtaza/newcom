package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ==========================================
// MODULE 3: Outbound MTA Traffic Shaper and SMTP Handshake Monitor
// ==========================================

// TokenBucketRateLimiter implements a robust, thread-safe token bucket algorithm
type TokenBucketRateLimiter struct {
	mu           sync.Mutex
	tokens       float64
	capacity     float64
	fillRate     float64 // Tokens added per second
	lastFilledAt time.Time
}

func NewTokenBucketRateLimiter(capacity, fillRate float64) *TokenBucketRateLimiter {
	return &TokenBucketRateLimiter{
		tokens:       capacity,
		capacity:     capacity,
		fillRate:     fillRate,
		lastFilledAt: time.Now(),
	}
}

func (tb *TokenBucketRateLimiter) SetFillRate(newRate float64) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refill()
	tb.fillRate = newRate
}

func (tb *TokenBucketRateLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastFilledAt).Seconds()
	tb.tokens += elapsed * tb.fillRate
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}
	tb.lastFilledAt = now
}

func (tb *TokenBucketRateLimiter) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refill()
	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

// SMTPSession represents the state of a single outbound handshake
type SMTPSession struct {
	IPAddress    string
	TargetDomain string
	Recipient    string
	Payload      []byte
	Client       *smtp.Client
}

// MTATrafficShaper governs the outbound IP fleet
type MTATrafficShaper struct {
	db             *sql.DB
	rdb            *redis.Client
	ipLimiters     map[string]*TokenBucketRateLimiter
	ipLimitersMu   sync.RWMutex
	anchorPool     []string // Warm IP addresses for fallback
}

func NewMTATrafficShaper(db *sql.DB, rdb *redis.Client, anchorIPs []string) *MTATrafficShaper {
	return &MTATrafficShaper{
		db:         db,
		rdb:        rdb,
		ipLimiters: make(map[string]*TokenBucketRateLimiter),
		anchorPool: anchorIPs,
	}
}

// getLimiter retrieves or initializes a token bucket for the specific IP
func (mta *MTATrafficShaper) getLimiter(ipAddress string) *TokenBucketRateLimiter {
	mta.ipLimitersMu.RLock()
	limiter, exists := mta.ipLimiters[ipAddress]
	mta.ipLimitersMu.RUnlock()

	if exists {
		return limiter
	}

	mta.ipLimitersMu.Lock()
	defer mta.ipLimitersMu.Unlock()
	
	// Double check pattern
	if limiter, exists := mta.ipLimiters[ipAddress]; exists {
		return limiter
	}

	// Default initialization: 10 capacity, 2 sends per second
	limiter = NewTokenBucketRateLimiter(10.0, 2.0)
	mta.ipLimiters[ipAddress] = limiter
	return limiter
}

// QuarantineWorkspace completely locks down a workspace upon severe spamhaus/550 violation
func (mta *MTATrafficShaper) QuarantineWorkspace(ctx context.Context, orgID string, maliciousDomain string) error {
	log.Printf("[SECURITY] Quarantine triggered for Org %s due to domain %s", orgID, maliciousDomain)
	
	// 1. Lock the organization in postgres
	_, err := mta.db.ExecContext(ctx, "UPDATE organizations SET status = 'QUARANTINED' WHERE id = $1", orgID)
	if err != nil {
		return err
	}

	// 2. Kill all in-flight session tokens for the org
	mta.rdb.Del(ctx, "auth:org:"+orgID)

	// 3. Trigger Admin Event Payload
	adminPayload := map[string]string{
		"org_id": orgID,
		"reason": "SMTP 550 Blocked Domain Trigger",
		"domain": maliciousDomain,
		"time":   time.Now().Format(time.RFC3339),
	}
	payloadBytes, _ := json.Marshal(adminPayload)
	mta.rdb.LPush(ctx, "admin_security_alerts", payloadBytes)

	return nil
}

// PerformHandshake establishes TCP and executes the SMTP sequence, returning explicit SMTP status codes
func (mta *MTATrafficShaper) PerformHandshake(session *SMTPSession) (int, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	// Only bind a specific source IP when one was actually configured for
	// this org (a real IP the operator owns, reachable and with its own
	// PTR/rDNS set up). Binding to an arbitrary address this host doesn't
	// own just fails the dial; letting the OS pick the default route is
	// the correct behavior when no dedicated IP applies.
	if session.IPAddress != "" {
		localAddr, err := net.ResolveTCPAddr("tcp", session.IPAddress+":0")
		if err != nil {
			return 0, fmt.Errorf("invalid source IP %s: %v", session.IPAddress, err)
		}
		dialer.LocalAddr = localAddr
	}

	// Lookup MX records for target domain
	mxs, err := net.LookupMX(session.TargetDomain)
	if err != nil || len(mxs) == 0 {
		return 550, fmt.Errorf("no MX records for %s", session.TargetDomain)
	}

	// Attempt connection to the primary MX
	targetServer := mxs[0].Host + ":25"
	conn, err := dialer.Dial("tcp", targetServer)
	if err != nil {
		return 421, fmt.Errorf("tcp dial failed: %v", err)
	}

	client, err := smtp.NewClient(conn, mxs[0].Host)
	if err != nil {
		return 421, fmt.Errorf("smtp handshake failed: %v", err)
	}
	defer client.Close()

	// mailHostname() is the same configurable hostname the inbound receiver
	// advertises (MAIL_HOSTNAME env var) -- "our-esp.com" was a fake
	// placeholder domain nobody deploying this actually owns, which would
	// fail HELO/rDNS alignment checks against any real receiver.
	if err = client.Hello(mailHostname()); err != nil {
		return extractSMTPCode(err), err
	}

	// Try STARTTLS if supported
	if ok, _ := client.Extension("STARTTLS"); ok {
		// In production, configure tls.Config properly
		if err = client.StartTLS(nil); err != nil {
			log.Printf("[MTA] STARTTLS failed or rejected by %s: %v", mxs[0].Host, err)
			// Continue unencrypted or return error depending on strictness
		}
	}

	// Sender & Rcpt
	if err = client.Mail("bounce@" + mailHostname()); err != nil {
		return extractSMTPCode(err), err
	}
	if err = client.Rcpt(session.Recipient); err != nil {
		return extractSMTPCode(err), err
	}

	// Data
	w, err := client.Data()
	if err != nil {
		return extractSMTPCode(err), err
	}
	_, err = w.Write(session.Payload)
	if err != nil {
		return extractSMTPCode(err), err
	}
	err = w.Close()
	if err != nil {
		return extractSMTPCode(err), err
	}

	return 250, nil
}

// extractSMTPCode safely unwraps net/smtp errors to isolate the 3-digit status code
func extractSMTPCode(err error) int {
	if err == nil {
		return 250
	}
	// Note: smtp.Error doesn't easily expose the raw code in all Go versions, parsing the string
	errStr := err.Error()
	if len(errStr) >= 3 {
		var code int
		fmt.Sscanf(errStr[:3], "%d", &code)
		if code >= 200 && code <= 599 {
			return code
		}
	}
	return 421 // Default to soft fail
}

// Dispatch processes the email delivery, enforcing rate limits and handling telemetry
func (mta *MTATrafficShaper) Dispatch(ctx context.Context, orgID string, session *SMTPSession) error {
	limiter := mta.getLimiter(session.IPAddress)

	// Rate Limiting Evaluation
	if !limiter.Allow() {
		// Token bucket exhausted. Try to find an available Anchor IP.
		foundAnchor := false
		for _, anchorIP := range mta.anchorPool {
			anchorLimiter := mta.getLimiter(anchorIP)
			if anchorLimiter.Allow() {
				session.IPAddress = anchorIP
				foundAnchor = true
				log.Printf("[Shaper] Capacity exhausted. Re-routing to Anchor IP %s", session.IPAddress)
				break
			}
		}
		if !foundAnchor {
			return fmt.Errorf("rate limited and no anchor IPs available/have capacity")
		}
	}

	// Execute actual SMTP connection
	statusCode, err := mta.PerformHandshake(session)

	// Conditional Handlers for SMTP Status Responses
	switch {
	case statusCode >= 200 && statusCode < 300:
		// Success
		return nil

	case statusCode == 421 || statusCode == 450 || statusCode == 451 || statusCode == 452:
		// Throttle Signal / Soft Bounce
		log.Printf("[MTA 421] Receiver throttled IP %s. Degrading velocity.", session.IPAddress)
		
		// Programmatically degrade the sending velocity of this specific IP node
		mta.ipLimitersMu.RLock()
		if currentLimiter, exists := mta.ipLimiters[session.IPAddress]; exists {
			// Reduce fill rate by 50%
			currentLimiter.SetFillRate(currentLimiter.fillRate * 0.5)
		}
		mta.ipLimitersMu.RUnlock()

		return fmt.Errorf("SMTP Throttle %d: %v", statusCode, err)

	case statusCode >= 500 && statusCode <= 599:
		// Hard Exception / Block
		log.Printf("[MTA 550] Fatal bounce for %s from %s. Possible spam block.", session.Recipient, session.TargetDomain)
		
		if orgID != "" {
			// Extract malicious domain from recipient
			parts := strings.Split(session.Recipient, "@")
			var domain string
			if len(parts) == 2 {
				domain = parts[1]
			}
			
			// Cleanly quarantine workspace and trigger admin event
			quarantineErr := mta.QuarantineWorkspace(ctx, orgID, domain)
			if quarantineErr != nil {
				log.Printf("CRITICAL: Failed to quarantine org %s: %v", orgID, quarantineErr)
			}
		}
		return fmt.Errorf("SMTP Block %d: %v", statusCode, err)

	default:
		return fmt.Errorf("unknown SMTP error %d: %v", statusCode, err)
	}
}
