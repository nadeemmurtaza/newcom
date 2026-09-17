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
	if query == "" || orgID == "" {
		http.Error(w, "q and org_id are required", http.StatusBadRequest)
		return
	}

	// Phase 28: LSM-Tree Search Index (Gmail Architecture)
	msgIDs := GlobalLSM.Search(query)
	if len(msgIDs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]SearchResult{})
		return
	}

	// Fetch metadata from Postgres (Simulated Spanner)
	// We need to format the slice into a postgres IN clause
	var results []SearchResult
	for _, id := range msgIDs {
		var res SearchResult
		err := db.QueryRow(`
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
