package main

import (
	"encoding/json"
	"net/http"
	"time"
)

type CalendarEvent struct {
	ID          string   `json:"id,omitempty"`
	OrgID       string   `json:"org_id"`
	OrganizerID string   `json:"organizer_id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	Attendees   []string  `json:"attendees"`
	MeetingLink string   `json:"meeting_link"`
}

func handleCalendarCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var ev CalendarEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	attendeesJSON, _ := json.Marshal(ev.Attendees)
	// Collision Detection (Checking if organizer is double-booked)
	var conflictCount int
	db.QueryRow(`
		SELECT count(*) FROM calendar_events 
		WHERE organizer_id = $1 
		  AND start_time < $2 AND end_time > $3`, 
		ev.OrganizerID, ev.EndTime, ev.StartTime).Scan(&conflictCount)
	
	if conflictCount > 0 {
		http.Error(w, "Double booked! Organizer is busy during this time.", http.StatusConflict)
		return
	}

	var eventID string
	err := db.QueryRow(`
		INSERT INTO calendar_events (org_id, organizer_id, title, description, start_time, end_time, attendees, meeting_link)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		ev.OrgID, ev.OrganizerID, ev.Title, ev.Description, ev.StartTime, ev.EndTime, attendeesJSON, "https://meet.our-esp.com/" + ev.OrganizerID[:8],
	).Scan(&eventID)

	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	ev.ID = eventID

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ev)
}
