package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type ZAPRequest struct {
	Sender         string `json:"sender"`
	TimeRangeStart string `json:"time_range_start"` // RFC3339
	TimeRangeEnd   string `json:"time_range_end"`
	SubjectContains string `json:"subject_contains"`
}

// handleZeroHourAutoPurge (ZAP) pulls malicious emails out of users' inboxes retroactively
func handleZeroHourAutoPurge(w http.ResponseWriter, r *http.Request) {
	var req ZAPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
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
	
	res, err := db.Exec(`
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
			  AND m.sender = $1
			  AND m.created_at >= $2::timestamp
			  AND m.created_at <= $3::timestamp
			  AND ($4 = '' OR m.subject ILIKE '%' || $4 || '%')
			RETURNING m.id, m.org_id
		)
		INSERT INTO message_traces (message_id, org_id, event_type, details)
		SELECT id, org_id, 'zap', jsonb_build_object('sender', $1::text, 'action', 'quarantined', 'subject_match', $4::text)
		FROM UpdatedMessages
	`, req.Sender, req.TimeRangeStart, req.TimeRangeEnd, req.SubjectContains)
	
	if err != nil {
		http.Error(w, "Database error during ZAP", http.StatusInternalServerError)
		return
	}
	
	rowsAffected, _ := res.RowsAffected()
	log.Printf("Security: ZAP completed. Quarantined %d messages.", rowsAffected)
	
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success", "quarantined_count": ` + string(rune(rowsAffected)) + `}`))
}
