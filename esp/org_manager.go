package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// Phase 37: Enterprise Organization System

type OrgManager struct {
	db *sql.DB
}

func NewOrgManager(db *sql.DB) *OrgManager {
	return &OrgManager{db: db}
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
func (om *OrgManager) GenerateImpersonationToken(adminSubAccountID string, targetOrgID string, ipAddress string) (string, error) {
	// 1. Verify that the admin is actually a "Global System Admin" 
	// For simulation, we check if their sub_account role is 'System Admin'
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

	// 3. Mint the time-bound token (Simulated JWT)
	token := fmt.Sprintf("jwt_impersonate_%s_%s_exp%d", adminSubAccountID, targetOrgID, time.Now().Add(1*time.Hour).Unix())
	
	return token, nil
}
