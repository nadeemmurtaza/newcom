package main

import (
	"encoding/json"
	"net/http"
)

type DashboardStats struct {
	TotalSent   int `json:"total_sent"`
	TotalOpens  int `json:"total_opens"`
	TotalClicks int `json:"total_clicks"`
	TotalBounces int `json:"total_bounces"`
}

func hasPermission(userID string, orgID string, requiredPermission string) bool {
	var exists bool
	err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM organization_user_permissions 
			WHERE user_id = $1 AND org_id = $2 AND permission = $3
		)
	`, userID, orgID, requiredPermission).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}

func handleDashboardStats(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org_id")
	userID := r.Header.Get("X-User-ID") // set by userAuthMiddleware (user_auth.go) from a verified session

	if orgID == "" || userID == "" {
		http.Error(w, "Missing org_id or X-User-ID", http.StatusBadRequest)
		return
	}

	// PBAC Check
	if !hasPermission(userID, orgID, "analytics:read") {
		http.Error(w, "Forbidden: Missing analytics:read permission", http.StatusForbidden)
		return
	}
	
	var stats DashboardStats
	
	// Filter events by joining sub_accounts which belongs to the org
	baseQuery := `
		SELECT COUNT(*) FROM events e 
		JOIN sub_accounts s ON e.sub_account_id = s.id 
		WHERE s.org_id = $1 AND e.event_type = $2`
		
	db.QueryRow(baseQuery, orgID, "delivered").Scan(&stats.TotalSent)
	db.QueryRow(baseQuery, orgID, "open").Scan(&stats.TotalOpens)
	db.QueryRow(baseQuery, orgID, "click").Scan(&stats.TotalClicks)
	db.QueryRow(baseQuery, orgID, "bounce").Scan(&stats.TotalBounces)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(stats)
}

type DomainResponse struct {
	ID         string `json:"id"`
	DomainName string `json:"domain_name"`
	IsVerified bool   `json:"is_verified"`
	CustomTrackingURL string `json:"custom_tracking_url"`
}

func handleDashboardDomains(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org_id")
	userID := r.Header.Get("X-User-ID")
	
	if orgID == "" || userID == "" {
		http.Error(w, "Missing org_id or X-User-ID", http.StatusBadRequest)
		return
	}

	// PBAC Check
	if !hasPermission(userID, orgID, "domains:read") {
		http.Error(w, "Forbidden: Missing domains:read permission", http.StatusForbidden)
		return
	}

	rows, err := db.Query("SELECT id, domain_name, is_verified, COALESCE(custom_tracking_url, '') FROM domains WHERE org_id = $1", orgID)
	if err != nil {
		http.Error(w, "Failed to query domains", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var domains []DomainResponse
	for rows.Next() {
		var d DomainResponse
		if err := rows.Scan(&d.ID, &d.DomainName, &d.IsVerified, &d.CustomTrackingURL); err == nil {
			domains = append(domains, d)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(domains)
}
