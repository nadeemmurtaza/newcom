package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Phase 37: Enterprise Organization System

const impersonationTokenTTL = 1 * time.Hour

type OrgManager struct {
	db  *sql.DB
	rdb *redis.Client
}

func NewOrgManager(db *sql.DB, rdb *redis.Client) *OrgManager {
	return &OrgManager{db: db, rdb: rdb}
}

// 1. SSO Enforcement Check
func (om *OrgManager) IsSSOEnforced(orgID string) bool {
	var enforced bool
	err := om.db.QueryRow("SELECT sso_enforced FROM organizations WHERE id = $1", orgID).Scan(&enforced)
	if err != nil {
		return false
	}
	return enforced
}

// 2. Hierarchical Validation (Is requester part of a Parent Org that manages the target Org?)
func (om *OrgManager) CanManageChildOrg(requesterSubAccountID string, targetOrgID string) bool {
	var requesterOrgID string
	var targetParentOrgID sql.NullString

	// Get the org of the requester
	err := om.db.QueryRow("SELECT org_id FROM sub_accounts WHERE id = $1", requesterSubAccountID).Scan(&requesterOrgID)
	if err != nil {
		return false
	}

	// Get the parent of the target org
	err = om.db.QueryRow("SELECT parent_org_id FROM organizations WHERE id = $1", targetOrgID).Scan(&targetParentOrgID)
	if err != nil {
		return false
	}

	// If the requester's org IS the parent org of the target, they are authorized (Agency Reseller Model)
	if targetParentOrgID.Valid && targetParentOrgID.String == requesterOrgID {
		return true
	}

	return false
}

// 3. Support Impersonation
//
// Phase 55: two real bugs here. (a) sub_accounts was joined to a "roles"
// table that never existed anywhere in schema.sql, so this errored on
// every single call regardless of who called it or what role they had --
// added the table in schema.sql. (b) the "token" was fmt.Sprintf'd plain
// text with the admin ID, target org ID and expiry baked in -- anyone
// could construct their own by copying the format, and worse, nothing
// anywhere ever checked this token against anything, so impersonation
// never actually granted access to begin with. It's now a real random
// token stored server-side in Redis (authMiddleware in main.go consumes
// it via VerifyImpersonationToken).
func (om *OrgManager) GenerateImpersonationToken(adminSubAccountID string, targetOrgID string, ipAddress string) (string, error) {
	var role string
	err := om.db.QueryRow(`
		SELECT r.name FROM sub_accounts s
		JOIN roles r ON s.role_id = r.id
		WHERE s.id = $1
	`, adminSubAccountID).Scan(&role)

	if err != nil || role != "System Admin" {
		return "", fmt.Errorf("Unauthorized: Only System Admins can impersonate")
	}

	// 2. Log the critical impersonation event to the immutable Audit Log
	payload := fmt.Sprintf(`{"target_org_id": "%s", "reason": "Support Ticket Investigation", "duration_mins": 60}`, targetOrgID)
	globalRulesEngine.LogAudit(targetOrgID, adminSubAccountID, "IMPERSONATION_STARTED", ipAddress, payload)

	log.Printf("SECURITY ALERT: System Admin %s initiated impersonation of Org %s from IP %s", adminSubAccountID, targetOrgID, ipAddress)

	// 3. Mint a real random token, keyed server-side -- not a string the
	// caller could ever reconstruct themselves.
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("failed to generate impersonation token: %w", err)
	}
	token := hex.EncodeToString(raw)

	value := adminSubAccountID + "|" + targetOrgID
	if err := om.rdb.Set(context.Background(), "impersonation:"+token, value, impersonationTokenTTL).Err(); err != nil {
		return "", fmt.Errorf("failed to store impersonation token: %w", err)
	}

	return token, nil
}

// VerifyImpersonationToken resolves a token minted above to the
// (adminSubAccountID, targetOrgID) pair it grants, if it's still valid.
func (om *OrgManager) VerifyImpersonationToken(token string) (adminSubAccountID, targetOrgID string, ok bool) {
	val, err := om.rdb.Get(context.Background(), "impersonation:"+token).Result()
	if err != nil || val == "" {
		return "", "", false
	}
	parts := strings.SplitN(val, "|", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}
