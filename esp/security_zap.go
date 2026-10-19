package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

type ZAPRequest struct {
	OrgID          string `json:"org_id"`
	Sender         string `json:"sender"`
	TimeRangeStart string `json:"time_range_start"` // RFC3339
	TimeRangeEnd   string `json:"time_range_end"`
	SubjectContains string `json:"subject_contains"`
}

// handleZeroHourAutoPurge (ZAP) pulls malicious emails out of users' inboxes
// retroactively.
//
// Phase 61: had no auth and no org scoping at all -- any caller could POST
// a sender and quarantine matching messages across every org on the
// platform. Now requires a verified session (userAuthMiddleware), a
// security:admin permission in the target org, and the purge query itself
// is scoped to that org's mailboxes only.
func handleZeroHourAutoPurge(w http.ResponseWriter, r *http.Request) {
	var req ZAPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	userID := r.Header.Get("X-User-ID") // set by userAuthMiddleware from a verified session
	if req.OrgID == "" {
		http.Error(w, "org_id is required", http.StatusBadRequest)
		return
	}
	if !hasPermission(userID, req.OrgID, "security:admin") {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if req.Sender == "" {
		http.Error(w, "Sender required for ZAP", http.StatusBadRequest)
		return
	}

	log.Printf("Security: Initiating Targeted Zero-hour Auto Purge (ZAP) for sender: %s", req.Sender)
	
	// Default time range if not provided
	if req.TimeRangeStart == "" { req.TimeRangeStart = "1970-01-01T00:00:00Z" }
	if req.TimeRangeEnd == "" { req.TimeRangeEnd = "2100-01-01T00:00:00Z" }
	
	// Deep Logic: Move to Quarantine and flag as phishing instead of deleting
	// We need to resolve the "Quarantine" folder for each mailbox that has a message from this sender
	// Using a complex CTE to ensure the Quarantine folder exists or is selected, then updating.
	//
	// Phase 61: wrapped in a transaction with app.current_org_id set, like
	// every other messages access -- without it the tenant_isolation_policy
	// RLS policy silently denies every row, same bug class fixed elsewhere
	// in this codebase. Also scoped to m.org_id = the caller's own org
	// (previously this matched the sender across every org on the
	// platform), and the response bug that encoded rowsAffected as a
	// Unicode codepoint instead of a decimal number is fixed.
	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Database error during ZAP", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec("SELECT set_config('app.current_org_id', $1, true)", req.OrgID); err != nil {
		http.Error(w, "Database error during ZAP", http.StatusInternalServerError)
		return
	}

	res, err := tx.Exec(`
		WITH QuarantineFolders AS (
			SELECT f.id, f.mailbox_id
			FROM folders f
			WHERE f.name = 'Quarantine'
		),
		UpdatedMessages AS (
			UPDATE messages m
			SET folder_id = q.id, phishing_flag = TRUE
			FROM QuarantineFolders q
			WHERE m.mailbox_id = q.mailbox_id
			  AND m.org_id = $5
			  AND m.sender = $1
			  AND m.created_at >= $2::timestamp
			  AND m.created_at <= $3::timestamp
			  AND ($4 = '' OR m.subject ILIKE '%' || $4 || '%')
			RETURNING m.id, m.org_id
		)
		INSERT INTO message_traces (message_id, org_id, event_type, details)
		SELECT id, org_id, 'zap', jsonb_build_object('sender', $1::text, 'action', 'quarantined', 'subject_match', $4::text)
		FROM UpdatedMessages
	`, req.Sender, req.TimeRangeStart, req.TimeRangeEnd, req.SubjectContains, req.OrgID)

	if err != nil {
		http.Error(w, "Database error during ZAP", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Database error during ZAP", http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := res.RowsAffected()
	log.Printf("Security: ZAP completed. Quarantined %d messages.", rowsAffected)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"success", "quarantined_count": %d}`, rowsAffected)
}
