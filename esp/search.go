package main

import (
	"encoding/json"
	"net/http"
)

type SearchResult struct {
	MessageID string `json:"message_id"`
	Subject   string `json:"subject"`
	Sender    string `json:"sender"`
	Snippet   string `json:"snippet"`
	Rank      float32 `json:"rank"`
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	orgID := r.URL.Query().Get("org_id")
	userID := r.Header.Get("X-User-ID") // set by userAuthMiddleware from a verified session
	if query == "" || orgID == "" {
		http.Error(w, "q and org_id are required", http.StatusBadRequest)
		return
	}
	if !hasPermission(userID, orgID, "mail:read") {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Phase 28: LSM-Tree Search Index (Gmail Architecture)
	msgIDs := GlobalLSM.Search(query)
	if len(msgIDs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]SearchResult{})
		return
	}

	// Fetch metadata from Postgres.
	//
	// Phase 55: this was a plain db.QueryRow against messages, which (like
	// every other unscoped query against that table) the
	// tenant_isolation_policy RLS policy always denies -- every search
	// silently returned zero results no matter what matched in the LSM
	// index, because the Postgres lookup for each match's metadata always
	// failed and was swallowed by `if err == nil`. Scoped to a transaction
	// with app.current_org_id set, like every other messages access.
	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec("SELECT set_config('app.current_org_id', $1, true)", orgID); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	var results []SearchResult
	for _, id := range msgIDs {
		var res SearchResult
		err := tx.QueryRow(`
			SELECT m.id, m.subject, m.sender, m.snippet, 1.0 AS rank
			FROM messages m
			JOIN mailboxes mb ON m.mailbox_id = mb.id
			WHERE mb.org_id = $1 AND m.id = $2
		`, orgID, id).Scan(&res.MessageID, &res.Subject, &res.Sender, &res.Snippet, &res.Rank)

		if err == nil {
			results = append(results, res)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}
