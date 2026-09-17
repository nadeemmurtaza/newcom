package main

import (
	"encoding/json"
	"net/http"
)

type Template struct {
	ID              string `json:"id,omitempty"`
	Name            string `json:"name"`
	SubjectTemplate string `json:"subject_template"`
	HtmlContent     string `json:"html_content"`
	TextContent     string `json:"text_content"`
}

func handleTemplates(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org_id")
	userID := r.Header.Get("X-User-ID")

	if orgID == "" || userID == "" {
		http.Error(w, "Missing org_id or X-User-ID", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	switch r.Method {
	case "GET":
		// Must have templates:read
		if !hasPermission(userID, orgID, "templates:read") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		rows, err := db.Query("SELECT id, name, subject_template, html_content, text_content FROM templates WHERE org_id = $1", orgID)
		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var tmpls []Template
		for rows.Next() {
			var t Template
			if err := rows.Scan(&t.ID, &t.Name, &t.SubjectTemplate, &t.HtmlContent, &t.TextContent); err == nil {
				tmpls = append(tmpls, t)
			}
		}
		json.NewEncoder(w).Encode(tmpls)

	case "POST":
		// Must have templates:write
		if !hasPermission(userID, orgID, "templates:write") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		var t Template
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}

		err := db.QueryRow(`
			INSERT INTO templates (org_id, name, subject_template, html_content, text_content) 
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, 
			orgID, t.Name, t.SubjectTemplate, t.HtmlContent, t.TextContent).Scan(&t.ID)
		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(t)

	case "OPTIONS":
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-User-ID")
		w.WriteHeader(http.StatusOK)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
