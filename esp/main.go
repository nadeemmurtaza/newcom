package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/webhook"
)

var db *sql.DB
var rdb *redis.Client
var authCache *AuthCache
var rateLimiter *RateLimiter
var idempotencyEngine *IdempotencyEngine
var globalRulesEngine *RulesEngine
var tenantManager *TenantManager
var globalOrgManager *OrgManager
var mailboxManager *MailboxManager
var globalBrandingManager *BrandingManager
var globalPostmaster *PostmasterManager
var globalJourneyEngine *JourneyEngine
var globalBillingEngine *BillingEngine
var globalMLScanner *MLAbuseScanner
var globalMTAShaper *MTATrafficShaper

// Phase 23: Global Worker Sync state
var globalWorkerCtx context.Context
var globalWorkerWg sync.WaitGroup

type SendEmailRequest struct {
	MessageID    string                 `json:"message_id,omitempty"`
	SubAccountID string                 `json:"sub_account_id,omitempty"`
	OrgID        string                 `json:"org_id,omitempty"`
	To           string                 `json:"to"`
	From         string                 `json:"from"`
	Subject      string                 `json:"subject,omitempty"` // Now optional if using template
	Body         string                 `json:"body,omitempty"`
	CustomHeaders map[string]string     `json:"custom_headers,omitempty"`
	TemplateID   string                 `json:"template_id,omitempty"`
	TemplateData map[string]interface{} `json:"template_data,omitempty"`
	SendAt       *int64                 `json:"send_at,omitempty"`
}

type SendEmailResponse struct {
	MessageID string `json:"message_id"`
	Status    string `json:"status"`
}

func main() {
	var err error
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://esp_user:esp_password@localhost:5432/esp_db?sslmode=disable"
	}
	db, err = sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Phase 22: Deep Enterprise Architecture - Connection Pool Limits
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Validate connection
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := db.PingContext(pingCtx); err != nil {
		log.Printf("Warning: Database ping failed: %v", err)
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	}
	rdb = redis.NewClient(opt)
	authCache = NewAuthCache(rdb)
	rateLimiter = NewRateLimiter(rdb)
	idempotencyEngine = NewIdempotencyEngine(rdb)
	tenantManager = NewTenantManager(db, rdb)
	globalOrgManager = NewOrgManager(db, rdb)
	mailboxManager = NewMailboxManager(db, rdb)
	globalBrandingManager = NewBrandingManager(db, rdb)
	globalPostmaster = NewPostmasterManager(rdb, db)
	globalJourneyEngine = NewJourneyEngine(db, rdb)
	globalBillingEngine = NewBillingEngine(db, rdb)
	globalMLScanner = NewMLAbuseScanner(db)

	// Phase 23: Global Worker Sync (Graceful Thread Termination)
	var workerCancel context.CancelFunc
	globalWorkerCtx, workerCancel = context.WithCancel(context.Background())

	// Phase 25: Campaign Bootloader moved to Java Spring Boot EventListener

	// Phase 28: Initialize LSM-Tree Search Index
	GlobalLSM = NewLSMTree("./lsm_data")

	// Phase 29: Initialize Outbound MTA Traffic Router
	outboundRouter := NewOutboundRouter()

	// Phase 53: Real outbound MTA (DNS MX lookup + SMTP handshake)
	globalMTAShaper = NewMTATrafficShaper(db, rdb, nil)

	// Phase 30: Initialize Identity Risk Engine (Kill Switch)
	riskEngine := NewIdentityRiskEngine(db, rdb)

	// Phase 55: startHeavyGuardWorker/startDeliveryWorker removed -- they
	// drained "email_inbound_queue", which nothing ever fed (the real
	// inbound path, startInboundSMTPServer, writes straight to Postgres).
	// See gmail_pipeline.go.
	globalWorkerWg.Add(1)
	go startSearchIndexerWorker(globalWorkerCtx, &globalWorkerWg, db, rdb, GlobalLSM)
	
	// Phase 38: Continuous DNS Verification Worker
	globalWorkerWg.Add(1)
	go startContinuousDNSWorker(globalWorkerCtx, &globalWorkerWg, db)

	globalWorkerWg.Add(1)
	go startOutboundMTAWorker(globalWorkerCtx, &globalWorkerWg, db, rdb, outboundRouter, riskEngine)

	// Phase 40: Delayed Queue Poller
	globalWorkerWg.Add(1)
	go startDelayedQueueWorker(globalWorkerCtx, &globalWorkerWg, rdb)

	// Phase 42: Auto Expunge Worker
	globalWorkerWg.Add(1)
	go startAutoExpungeWorker(globalWorkerCtx, &globalWorkerWg, db)

	// Phase 44: Journey Builder Engine
	globalWorkerWg.Add(1)
	go startJourneyWorker(globalWorkerCtx, &globalWorkerWg, db, rdb)

	// Phase 45: A/B Split Testing Evaluator
	globalWorkerWg.Add(1)
	go startABTestEvaluator(globalWorkerCtx, &globalWorkerWg, db, rdb)

	// Phase 46: Dedicated IP Warmup Engine
	globalWorkerWg.Add(1)
	go startIPWarmupCron(globalWorkerCtx, &globalWorkerWg, db)

	// Phase 50: Real Inbound SMTP Receiver (replaces the never-finished C++ stub)
	globalWorkerWg.Add(1)
	go startInboundSMTPServer(globalWorkerCtx, &globalWorkerWg, db, rdb)

	go StartScheduler(globalWorkerCtx, &globalWorkerWg, db, rdb)

	// Phase 31: Initialize Rules Engine & Context-Aware Access
	globalRulesEngine = NewRulesEngine(db, riskEngine)

	mux := http.NewServeMux()

	// Serve the frontend dashboard
	mux.Handle("/", http.FileServer(http.Dir("./public")))

	// Simulated Login Endpoint (Activity Rules intercept)
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		orgID := r.URL.Query().Get("org_id")
		accountID := r.URL.Query().Get("account_id")
		ipAddress := r.RemoteAddr // in a real app, parse X-Forwarded-For

		// Phase 41: White-Label Portal Security Check
		if config, ok := r.Context().Value(BrandingContextKey).(*BrandingConfig); ok {
			if config.OrgID != orgID {
				http.Error(w, "403 Forbidden: You cannot log into this organization from this portal URL.", http.StatusForbidden)
				return
			}
		}

		// Phase 37: SSO Enforcement Intercept
		if globalOrgManager.IsSSOEnforced(orgID) {
			http.Error(w, "SSO Enforced: Please login via your corporate Identity Provider (SAML/OIDC).", http.StatusForbidden)
			return
		}

		// Evaluate Identity Risk Context
		riskEngine.EvaluateLoginAnomaly(accountID, ipAddress, r.UserAgent())

		// Evaluate Rules Engine (Strict perimeter lock)
		if !globalRulesEngine.EvaluateLogin(orgID, accountID, ipAddress) {
			http.Error(w, "Account Suspended by Organizational Policy", http.StatusForbidden)
			return
		}
		w.Write([]byte("Login Success. JWT Token Minted."))
	})

	// Phase 37: Secure Support Impersonation
	mux.HandleFunc("POST /api/support/impersonate", authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		adminSubAccountID := r.Header.Get("X-SubAccount-ID")
		targetOrgID := r.URL.Query().Get("target_org_id")

		token, err := globalOrgManager.GenerateImpersonationToken(adminSubAccountID, targetOrgID, r.RemoteAddr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"status": "SUCCESS", "impersonation_token": "%s", "expires_in": 3600}`, token)))
	}))

	// Simulated Context-Aware Access Endpoint
	mux.HandleFunc("/api/drive/secure-download", func(w http.ResponseWriter, r *http.Request) {
		orgID := r.URL.Query().Get("org_id")
		
		// Phase 32: Parse Device Posture from Headers
		ctx := RequestContext{
			IP:                r.RemoteAddr,
			IsManaged:         r.Header.Get("X-Device-Managed") == "true",
			ScreenLockEnabled: r.Header.Get("X-Device-ScreenLock") == "true",
		}

		// Evaluate Context-Aware Access Zero-Trust Policy for Google Drive
		if !globalRulesEngine.EvaluateCAA(orgID, "Google Drive", ctx) {
			http.Error(w, "Context-Aware Access: You cannot access Drive from this network/device.", http.StatusForbidden)
			return
		}
		w.Write([]byte("Secure File Payload"))
	})
	mux.HandleFunc("POST /v1/send", authMiddleware(handleSendEmail))
	// Phase 35: Campaign Maker-Checker Endpoints
	mux.HandleFunc("POST /v1/campaigns/send", authMiddleware(handleSendCampaign))
	mux.HandleFunc("POST /v1/campaigns/approve", authMiddleware(handleApproveCampaign))
	
	// Phase 39: Inbound Route
	mux.HandleFunc("POST /v1/inbound", handleInboundEmail)

	// Phase 50: Domain & Mailbox Provisioning (unlimited mailboxes per domain)
	mux.HandleFunc("POST /api/domains", authMiddleware(handleCreateDomain))
	mux.HandleFunc("GET /api/domains/{id}", authMiddleware(handleGetDomain))
	mux.HandleFunc("POST /api/domains/{id}/mailboxes", authMiddleware(handleCreateMailbox))
	mux.HandleFunc("GET /api/domains/{id}/mailboxes", authMiddleware(handleListMailboxes))
	mux.HandleFunc("DELETE /api/mailboxes/{id}", authMiddleware(handleDeleteMailbox))

	mux.HandleFunc("GET /health", handleHealthCheck)
	
	// Phase 3: Tracking & Bounces
	mux.HandleFunc("GET /track/open/", handleTrackOpen)
	mux.HandleFunc("GET /track/click/", handleTrackClick)
	mux.HandleFunc("POST /v1/webhooks/bounce", handleBounceWebhook)

	// Phase 43: Postmaster Telemetry & FBL
	mux.HandleFunc("GET /api/postmaster/stats", handlePostmasterStats)
	mux.HandleFunc("POST /v1/webhooks/fbl", handleFeedbackLoopWebhook)

	// Phase 44: Journey Builder Trigger
	mux.HandleFunc("POST /v1/journeys/trigger", authMiddleware(handleTriggerJourney))

	// Phase 47: Billing Webhook
	mux.HandleFunc("POST /v1/webhooks/stripe", handleStripeWebhook)

	// Phase 4: Dashboard API
	// Phase 55: real session auth (user_auth.go) instead of trusting a
	// client-supplied X-User-ID header for anything.
	mux.HandleFunc("POST /api/users/login", handleUserLogin)
	mux.HandleFunc("GET /api/stats", userAuthMiddleware(handleDashboardStats))
	mux.HandleFunc("GET /api/domains", userAuthMiddleware(handleDashboardDomains))

	// Phase 6: Mailbox API
	// Phase 52: every handler below now enforces that the session's mailbox
	// owns the mailbox_id/message_id it's touching; see mailbox_auth.go.
	mux.HandleFunc("POST /api/mailbox/login", handleMailboxLogin)
	mux.HandleFunc("GET /api/mailbox/messages", mailboxAuthMiddleware(handleGetMessages))
	mux.HandleFunc("PUT /api/mailbox/messages/", mailboxAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/star") {
			handleToggleStar(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/move") {
			handleMoveMessage(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/snooze") {
			handleSnoozeMessage(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/label") {
			handleApplyLabel(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/pin") {
			handlePinMessage(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/ignore") {
			handleIgnoreThread(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/recall") {
			handleRecallMessage(w, r)
		} else {
			http.NotFound(w, r)
		}
	}))

	mux.HandleFunc("DELETE /api/mailbox/messages/", mailboxAuthMiddleware(handleDeleteMessage))

	// Phase 7: Templates API
	mux.HandleFunc("/api/templates", userAuthMiddleware(handleTemplates))

	// Phase 8: Webhooks API
	mux.HandleFunc("/api/webhooks", userAuthMiddleware(handleWebhooks))

	// Phase 24: Workspace Ecosystem
	// Phase 55: this had no auth at all -- any caller could pass any
	// org_id and search that org's mail (moot in practice until the RLS
	// fix above, since every result was silently empty, but still a real
	// IDOR once that's fixed).
	mux.HandleFunc("GET /api/search", userAuthMiddleware(handleSearch))
	mux.HandleFunc("POST /api/drive/upload", handleDriveUpload)
	mux.HandleFunc("POST /api/calendar/events", handleCalendarCreate)

	// Phase 18: Security (ZAP)
	mux.HandleFunc("POST /api/security/zap", handleZeroHourAutoPurge)

	// Phase 10: Advanced Auth (DKIM & DNS)
	mux.HandleFunc("POST /api/domains/", authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/setup") {
			handleDomainSetup(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/verify") {
			handleDomainVerify(w, r)
		} else {
			http.NotFound(w, r)
		}
	}))

	// Phase 11: Java Migration - Campaigns are now handled by campaign-service (Port 8082)

	// Expose Prometheus Metrics
	mux.Handle("/metrics", promhttp.Handler())

	// Phase 25 originally planned to move SMTP receiving to a C++ engine on
	// :2525; that engine never got past a commented-out socket-call stub
	// (see git history, smtp-engine/ -- removed Phase 53). The real
	// receiver lives in this binary; see startInboundSMTPServer in
	// inbound_smtp.go, started below alongside the other workers.

	// Start Webhook Dispatcher
	globalWorkerWg.Add(1)
	go StartWebhookDispatcher(globalWorkerCtx, &globalWorkerWg, db, rdb)

	// Phase 41: White-Label Branding Endpoint
	mux.HandleFunc("GET /v1/branding", func(w http.ResponseWriter, r *http.Request) {
		config := r.Context().Value(BrandingContextKey)
		if config == nil {
			http.Error(w, "404 Not a White-Label Domain", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(config)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	addr := fmt.Sprintf(":%s", port)
	
	// Phase 41: Wrap the multiplexer in the BrandingMiddleware
	handler := globalBrandingManager.BrandingMiddleware(mux)
	
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	// Channel to listen for OS signals
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Starting ESP Ingestion API on %s...", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Block until a signal is received.
	<-stopChan
	log.Println("Received shutdown signal. Gracefully draining connections...")

	// Create a deadline to wait for.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	// Doesn't block if no connections, but will otherwise wait
	// until the timeout deadline.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("HTTP Server Shutdown Error: %v", err)
	}
	
	// Phase 23: Wait for all background workers to finish their current payload safely
	log.Println("Stopping background workers...")
	workerCancel()
	globalWorkerWg.Wait()
	
	// Close resources
	db.Close()
	rdb.Close()
	
	log.Println("Zero-Loss Shutdown Complete. Exiting.")
}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Phase 55: this is the actual consumer for tokens minted by
		// GenerateImpersonationToken (org_manager.go). Before this, nothing
		// anywhere checked that token for anything -- a System Admin could
		// call /api/support/impersonate, get a token back, and it granted
		// precisely nothing. An admin session presenting a valid token acts
		// with the impersonated org's identity for this request.
		if impToken := r.Header.Get("X-Impersonation-Token"); impToken != "" {
			adminSubAccountID, targetOrgID, ok := globalOrgManager.VerifyImpersonationToken(impToken)
			if !ok {
				http.Error(w, "Unauthorized: Invalid or expired impersonation token", http.StatusUnauthorized)
				return
			}
			r.Header.Set("X-SubAccount-ID", adminSubAccountID)
			r.Header.Set("X-Org-ID", targetOrgID)
			next.ServeHTTP(w, r)
			return
		}

		apiKey := r.Header.Get("Authorization")
		if apiKey == "" {
			http.Error(w, "Unauthorized: Missing API Key", http.StatusUnauthorized)
			return
		}

		// Phase 34: Global Rate Limiting (Token Bucket per API Key)
		// Max 1000 requests per minute
		if !rateLimiter.Allow(r.Context(), "api_key:"+apiKey, 1000, 1*time.Minute) {
			http.Error(w, "429 Too Many Requests: Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		// Phase 33: Check Redis Cache First
		var subAccountID, orgID, region string
		var quota int
		cachedVal, err := authCache.Get(r.Context(), apiKey)
		if err == nil && cachedVal != "" {
			parts := strings.Split(cachedVal, ",")
			if len(parts) == 4 {
				subAccountID = parts[0]
				orgID = parts[1]
				region = parts[2]
				fmt.Sscanf(parts[3], "%d", &quota)
			}
		}

		if subAccountID == "" {
			err = db.QueryRow(`
				SELECT a.sub_account_id, s.org_id, o.data_region, o.monthly_email_quota 
				FROM api_keys a
				JOIN sub_accounts s ON a.sub_account_id = s.id
				JOIN organizations o ON s.org_id = o.id
				WHERE a.key_hash = $1
			`, apiKey).Scan(&subAccountID, &orgID, &region, &quota)
			
			if err != nil {
				log.Printf("Auth failed for key: %v", err)
				http.Error(w, "Unauthorized: Invalid API Key", http.StatusUnauthorized)
				return
			}
			// Cache the success
			authCache.Set(r.Context(), apiKey, fmt.Sprintf("%s,%s,%s,%d", subAccountID, orgID, region, quota))
		}

		// Phase 36: Data Residency (Region) Check
		// Assuming this server instance is running in US-EAST for simulation
		if !tenantManager.EnsureRegion("US-EAST", region) {
			http.Error(w, "451 Unavailable For Legal Reasons: Tenant data must reside in " + region, 451)
			return
		}

		// Phase 47: Billing Account Suspension Check
		if !globalBillingEngine.IsAccountActive(orgID) {
			http.Error(w, "402 Payment Required: Organization account is PAST_DUE. Please update your billing info.", http.StatusPaymentRequired)
			return
		}

		// Phase 47: High-Speed Metered Quota Enforcement
		if !globalBillingEngine.CheckAndRecordUsage(r.Context(), orgID) {
			http.Error(w, "429 Too Many Requests: Monthly Tenant Quota Exceeded for your Billing Tier.", http.StatusTooManyRequests)
			return
		}

		r.Header.Set("X-SubAccount-ID", subAccountID)
		r.Header.Set("X-Org-ID", orgID)
		next.ServeHTTP(w, r)
	}
}

func handleSendEmail(w http.ResponseWriter, r *http.Request) {
	// Phase 34: Idempotency Check
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if cachedResponse, exists := idempotencyEngine.Check(r.Context(), idempotencyKey); exists {
		log.Printf("Idempotency Engine: Intercepted retry for %s, returning cached response", idempotencyKey)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted) // Must match original success code
		w.Write([]byte(cachedResponse))
		return
	}

	var req SendEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.To == "" || req.From == "" || req.Subject == "" || req.Body == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	req.SubAccountID = r.Header.Get("X-SubAccount-ID")
	req.OrgID = r.Header.Get("X-Org-ID")
	
	// Phase 39: Mailbox Velocity Limit
	var mailboxID string
	err := db.QueryRow("SELECT id FROM mailboxes WHERE email_address = $1", req.From).Scan(&mailboxID)
	if err == nil {
		if !mailboxManager.CheckVelocityLimit(r.Context(), mailboxID) {
			http.Error(w, "429 Too Many Requests: Mailbox daily send limit exceeded", http.StatusTooManyRequests)
			return
		}
	}

	log.Printf("Received email request from %s to %s (SubAccount: %s)", req.From, req.To, req.SubAccountID)

	// Generate MessageID
	msgID := "msg_" + fmt.Sprintf("%d", time.Now().UnixNano())
	req.MessageID = msgID

	// Publish to Redis Queue
	reqJSON, err := json.Marshal(req)
	if err != nil {
		http.Error(w, "Failed to serialize request", http.StatusInternalServerError)
		return
	}
	// Phase 12 & 14: Scheduled Sending and Undo Send Delay
	// Default to 10 seconds delay if SendAt is not provided (Undo Send window)
	var sendScore float64
	if req.SendAt != nil {
		sendScore = float64(*req.SendAt)
	} else {
		sendScore = float64(time.Now().Add(10 * time.Second).Unix())
	}

	err = rdb.ZAdd(r.Context(), "email_scheduled_queue", redis.Z{Score: sendScore, Member: reqJSON}).Err()
	if err != nil {
		log.Printf("Failed to push to scheduled queue: %v", err)
		http.Error(w, "Failed to queue email", http.StatusInternalServerError)
		return
	}

	// Response
	resp := SendEmailResponse{
		MessageID: msgID,
		Status:    "queued",
	}

	respBytes, _ := json.Marshal(resp)

	// Phase 34: Save Idempotency
	if idempotencyKey != "" {
		idempotencyEngine.Save(r.Context(), idempotencyKey, string(respBytes))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	w.Write(respBytes)
}

func handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// Phase 35: Maker-Checker Campaign Endpoint
func handleSendCampaign(w http.ResponseWriter, r *http.Request) {
	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	
	subAccountID := r.Header.Get("X-SubAccount-ID")
	orgID := r.Header.Get("X-Org-ID")
	
	recipientCount := 0
	if rcpts, ok := req["Recipients"].([]interface{}); ok {
		recipientCount = len(rcpts)
	} else {
		recipientCount = 15000 // Simulation for testing threshold
	}

	reqJSON, _ := json.Marshal(req)
	
	// Evaluate Maker-Checker Workflow
	if globalRulesEngine.InterceptLargeCampaign(orgID, subAccountID, string(reqJSON), recipientCount) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status": "PENDING_APPROVAL", "message": "Campaign exceeds 10,000 recipients. Requires Super Admin approval."}`))
		return
	}

	// Phase 45: A/B Split Testing Detection
	if isAB, ok := req["is_ab_test"].(bool); ok && isAB {
		// Mock inserting AB test into DB and slicing audience
		log.Printf("[A/B Engine] Campaign flagged for A/B testing. Slicing 20%% of audience for variants A and B.")
		// We would normally insert into ab_tests here
	} else {
		// If approved or small, log audit and proceed to queue
		globalRulesEngine.LogAudit(orgID, subAccountID, "CAMPAIGN_QUEUED", r.RemoteAddr, string(reqJSON))
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status": "QUEUED"}`))
}

// Phase 35: Super Admin Campaign Approval
func handleApproveCampaign(w http.ResponseWriter, r *http.Request) {
	subAccountID := r.Header.Get("X-SubAccount-ID")
	orgID := r.Header.Get("X-Org-ID")
	
	// MFA Verification for sensitive action
	if !globalRulesEngine.EvaluateMFA(subAccountID) {
		http.Error(w, "MFA Required: You must enable MFA to approve campaigns.", http.StatusForbidden)
		return
	}

	campaignID := r.URL.Query().Get("id")
	if campaignID == "" {
		http.Error(w, "Missing campaign id", http.StatusBadRequest)
		return
	}

	_, err := db.Exec(`
		UPDATE approval_requests 
		SET status = 'APPROVED', approver_id = $1, resolved_at = NOW() 
		WHERE id = $2 AND org_id = $3
	`, subAccountID, campaignID, orgID)

	if err != nil {
		http.Error(w, "Failed to approve campaign", http.StatusInternalServerError)
		return
	}

	globalRulesEngine.LogAudit(orgID, subAccountID, "CAMPAIGN_APPROVED", r.RemoteAddr, `{"campaign_id": "`+campaignID+`"}`)
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "APPROVED", "message": "Campaign released to dispatch queue."}`))
}

// Phase 39: Inbound Email Routing (Aliases & Catch-Alls)
//
// This HTTP path exists for inbound mail delivered by an external relay or
// MTA that prefers to hand off a parsed message over HTTP instead of
// speaking raw SMTP to this process. The raw-SMTP path is startInboundSMTPServer
// in inbound_smtp.go; both share persistInboundMessage so delivery is
// identical either way.
func handleInboundEmail(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainID string `json:"domain_id"`
		To       string `json:"to"`
		From     string `json:"from"`
		Subject  string `json:"subject"`
		Body     string `json:"body"`
		BodyHTML string `json:"body_html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	var orgID string
	if err := db.QueryRow("SELECT org_id FROM domains WHERE id = $1", req.DomainID).Scan(&orgID); err != nil {
		log.Printf("Inbound Route Failed: Unknown domain_id %s", req.DomainID)
		http.Error(w, "550 No such domain here", http.StatusNotFound)
		return
	}

	threadID := uuid.NewString()
	if err := persistInboundMessage(db, rdb, orgID, req.DomainID, req.From, req.To, req.To, req.Subject, req.Body, req.BodyHTML, threadID); err != nil {
		log.Printf("Inbound Route Failed: No mailbox, alias, or catch-all found for %s: %v", req.To, err)
		http.Error(w, "550 No such user here", http.StatusNotFound)
		return
	}

	log.Printf("Inbound Route Success: %s delivered", req.To)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "DELIVERED"}`))
}

// Phase 43: Postmaster Handlers
func handlePostmasterStats(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		http.Error(w, "Missing domain", http.StatusBadRequest)
		return
	}

	stats, err := globalPostmaster.GetStats(r.Context(), domain, 30)
	if err != nil {
		http.Error(w, "Failed to get stats", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func handleFeedbackLoopWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Recipient string `json:"recipient"`
		Sender    string `json:"sender"`
		Domain    string `json:"domain"`
		Reason    string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	log.Printf("[FBL] Received spam complaint from %s against %s", req.Recipient, req.Sender)

	// Increment spam counter
	globalPostmaster.RecordMetric(r.Context(), req.Domain, "spam", 1)

	// Push recipient to suppression list
	var orgID string
	db.QueryRow(`
		INSERT INTO suppression_list (org_id, email, reason) 
		SELECT org_id, $1, 'FBL_COMPLAINT' 
		FROM domains WHERE domain_name = $2 
		ON CONFLICT DO NOTHING RETURNING org_id
	`, req.Recipient, req.Domain).Scan(&orgID)

	if orgID == "" {
		// Fallback lookup if already suppressed
		db.QueryRow("SELECT org_id FROM domains WHERE domain_name = $1", req.Domain).Scan(&orgID)
	}

	if orgID != "" {
		meta := `{"email": "` + req.Recipient + `", "reason": "` + req.Reason + `"}`
		QueueWebhookEvent(rdb, WebhookEvent{OrgID: orgID, MessageID: "unknown", EventType: "complaint", Metadata: meta})
	}

	w.WriteHeader(http.StatusOK)
}

// Phase 44: Journey Trigger Handler
func handleTriggerJourney(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	if orgID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		JourneyID string `json:"journey_id"`
		Email     string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := globalJourneyEngine.TriggerJourney(orgID, req.JourneyID, req.Email); err != nil {
		http.Error(w, "Failed to trigger journey: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "SUCCESS", "message": "Contact injected into Journey successfully"}`))
}

// Phase 47/55: Stripe Webhook Handler
//
// This used to json.Decode the request body directly and act on whatever it
// said -- no signature check at all. Anyone who knew (or guessed) an org's
// Stripe customer ID could POST a fake "invoice.paid" to reactivate a
// suspended org for free, or a fake "invoice.payment_failed" to lock out a
// competitor. Now verifies the Stripe-Signature header against
// STRIPE_WEBHOOK_SECRET via the real Stripe SDK before touching anything.
func handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	webhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	if webhookSecret == "" {
		log.Printf("Stripe webhook rejected: STRIPE_WEBHOOK_SECRET is not configured")
		http.Error(w, "Webhook not configured", http.StatusServiceUnavailable)
		return
	}

	const maxStripeBodyBytes = 64 * 1024
	body, err := io.ReadAll(io.LimitReader(r.Body, maxStripeBodyBytes+1))
	if err != nil || len(body) > maxStripeBodyBytes {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	event, err := webhook.ConstructEvent(body, r.Header.Get("Stripe-Signature"), webhookSecret)
	if err != nil {
		log.Printf("Stripe webhook signature verification failed: %v", err)
		http.Error(w, "Invalid signature", http.StatusBadRequest)
		return
	}

	switch event.Type {
	case "invoice.payment_failed", "invoice.paid":
		var invoice stripe.Invoice
		if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
			log.Printf("Stripe webhook: failed to parse invoice payload: %v", err)
			http.Error(w, "Invalid event payload", http.StatusBadRequest)
			return
		}
		if invoice.Customer == nil || invoice.Customer.ID == "" {
			w.WriteHeader(http.StatusOK) // nothing to do, but it's a validly-signed event
			return
		}

		var orgID string
		if err := db.QueryRow("SELECT org_id FROM billing_subscriptions WHERE stripe_customer_id = $1", invoice.Customer.ID).Scan(&orgID); err == nil && orgID != "" {
			globalBillingEngine.ProcessStripeWebhook(string(event.Type), orgID, invoice.ID, int(invoice.AmountDue))
		}
	default:
		log.Printf("Stripe webhook: ignoring unhandled event type %s", event.Type)
	}

	w.WriteHeader(http.StatusOK)
}
