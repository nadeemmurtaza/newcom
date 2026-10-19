package main

import (
	"database/sql"
	"log"
	"net"
)

// Phase 31: Rules Engine & Context-Aware Access

type RulesEngine struct {
	db *sql.DB
	riskEngine *IdentityRiskEngine
}

func NewRulesEngine(db *sql.DB, riskEngine *IdentityRiskEngine) *RulesEngine {
	return &RulesEngine{db: db, riskEngine: riskEngine}
}

// 1. Activity Rules (The Kill Switch)
// Evaluated during the core Authentication / Login flow
func (re *RulesEngine) EvaluateLogin(orgID string, accountID string, ipAddress string) bool {
	// Query active Activity Rules for this Organization
	rows, err := re.db.Query(`
		SELECT condition_attribute, condition_operator, condition_value, action 
		FROM activity_rules 
		WHERE org_id = $1 AND is_active = true
	`, orgID)
	if err != nil {
		return true // Fail open if DB error in this simulation
	}
	defer rows.Close()

	clientIP := net.ParseIP(ipAddress)

	for rows.Next() {
		var attr, op, val, action string
		rows.Scan(&attr, &op, &val, &action)

		if attr == "IP_ADDRESS" && op == "NOT_IN_CIDR" {
			_, subnet, err := net.ParseCIDR(val)
			if err == nil {
				// If the user's IP is NOT in the allowed CIDR subnet, they violate the rule
				if !subnet.Contains(clientIP) {
					log.Printf("RULES ENGINE VIOLATION: Login from %s violates org %s strict IP perimeter.", ipAddress, orgID)
					
					if action == "SUSPEND_USER" {
						// Instantly escalate to the Kill Switch
						log.Printf("RULES ENGINE: Executing Hard Suspension for user %s", accountID)
						
						re.riskEngine.mu.Lock()
						// Force the risk score over the threshold
						re.riskEngine.riskMap[accountID] = 999 
						re.riskEngine.evaluateKillSwitch(accountID)
						re.riskEngine.mu.Unlock()
						
						return false // Login Denied
					}
				}
			}
		}
	}

	return true // Login Permitted
}

// Phase 32: Context Enriched payload for CEL evaluation
type RequestContext struct {
	IP                 string
	IsManaged          bool
	ScreenLockEnabled  bool
}

// 2. Context-Aware Access (Zero-Trust Dynamic App Blocking with CEL)
// Evaluated during HTTP middleware interceptors for specific high-value apps
func (re *RulesEngine) EvaluateCAA(orgID string, appName string, ctx RequestContext) bool {
	var celExpression string
	err := re.db.QueryRow(`
		SELECT cel_expression 
		FROM caa_policies 
		WHERE org_id = $1 AND app_name = $2 AND is_active = true
	`, orgID, appName).Scan(&celExpression)

	if err != nil {
		if err == sql.ErrNoRows {
			// No specific Zero-Trust policy for this app, permit access
			return true 
		}
		log.Printf("EvaluateCAA DB Error: %v", err)
		return false // Secure fail on error
	}

	// Phase 54: real CEL evaluation (github.com/google/cel-go) of whatever
	// expression the policy actually stores -- see cel_eval.go. This used
	// to hand-parse one specific hardcoded expression shape; any policy
	// phrased differently silently fell through to a device-only check.
	isAuthorized, err := evaluateCELPolicy(celExpression, ctx)
	if err != nil {
		log.Printf("CAA policy for org %s app %s failed to evaluate: %v. Secure-failing (denying access).", orgID, appName, err)
		return false
	}

	if !isAuthorized {
		log.Printf("CONTEXT-AWARE ACCESS BLOCKED: User at IP %s (Managed: %v) denied access to %s. Failed CEL: %s", 
			ctx.IP, ctx.IsManaged, appName, celExpression)
		return false // 403 Forbidden: App blocked, but account remains active
	}

	return true // Context verified, access granted
}

// ==========================================
// Phase 35: Enterprise Team Management
// ==========================================

// 1. MFA Enforcement
func (re *RulesEngine) EvaluateMFA(accountID string) bool {
	var mfaEnabled bool
	// Try users first
	err := re.db.QueryRow("SELECT mfa_enabled FROM users WHERE id = $1", accountID).Scan(&mfaEnabled)
	if err != nil {
		// Fallback to sub_accounts
		err = re.db.QueryRow("SELECT mfa_enabled FROM sub_accounts WHERE id = $1", accountID).Scan(&mfaEnabled)
		if err != nil {
			return false // Safe default: deny if not found
		}
	}
	return mfaEnabled
}

// 2. Maker-Checker Intercept
func (re *RulesEngine) InterceptLargeCampaign(orgID string, subAccountID string, payloadJSON string, recipientCount int) bool {
	// If a non-Super Admin tries to send > 10000 emails, intercept and queue for approval
	if recipientCount > 10000 {
		var role string
		err := re.db.QueryRow(`
			SELECT r.name FROM sub_accounts s
			JOIN roles r ON s.role_id = r.id
			WHERE s.id = $1
		`, subAccountID).Scan(&role)
		
		if err == nil && role != "Super Admin" {
			log.Printf("MAKER-CHECKER: Intercepting large campaign (%d recipients) from user %s. Requires Manager Approval.", recipientCount, subAccountID)
			re.db.Exec(`
				INSERT INTO approval_requests (org_id, requester_id, status, campaign_payload) 
				VALUES ($1, $2, 'PENDING_APPROVAL', $3)
			`, orgID, subAccountID, payloadJSON)
			return true // Campaign was intercepted
		}
	}
	return false // Allowed to proceed
}

// 3. Audit Logger
func (re *RulesEngine) LogAudit(orgID string, actorID string, action string, ip string, payload string) {
	_, err := re.db.Exec(`
		INSERT INTO audit_logs (org_id, actor_id, action, ip_address, payload) 
		VALUES ($1, $2, $3, $4, $5)
	`, orgID, actorID, action, ip, payload)
	
	if err != nil {
		log.Printf("AUDIT LOG FAILED (CRITICAL): %v", err)
	}
}
