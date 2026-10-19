package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// Phase 50: Domain Lifecycle - Create & Onboard

var domainNamePattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$`)

type DomainDNSRecord struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Value    string `json:"value"`
	Priority int    `json:"priority,omitempty"`
}

type CreateDomainResponse struct {
	ID         string            `json:"id"`
	DomainName string            `json:"domain_name"`
	Status     string            `json:"status"`
	DNSRecords []DomainDNSRecord `json:"dns_records"`
}

func mailHostname() string {
	if h := os.Getenv("MAIL_HOSTNAME"); h != "" {
		return h
	}
	return "mail.nmail.local"
}

// handleCreateDomain lets an org add a domain it will send/receive mail for.
// POST /api/domains  body: {"domain_name": "example.com"}
func handleCreateDomain(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	userID := r.Header.Get("X-User-ID")
	if orgID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if userID != "" && !hasPermission(userID, orgID, "domains:write") {
		http.Error(w, "Forbidden: Missing domains:write permission", http.StatusForbidden)
		return
	}

	var req struct {
		DomainName string `json:"domain_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	domainName := strings.ToLower(strings.TrimSpace(req.DomainName))
	if !domainNamePattern.MatchString(domainName) {
		http.Error(w, "Invalid domain_name", http.StatusBadRequest)
		return
	}

	var domainID string
	err := db.QueryRow(`
		INSERT INTO domains (org_id, domain_name, status)
		VALUES ($1, $2, 'PENDING')
		RETURNING id
	`, orgID, domainName).Scan(&domainID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			http.Error(w, "Domain already exists", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create domain: "+err.Error(), http.StatusInternalServerError)
		return
	}

	privPEM, pubB64, err := generateDKIMKeys()
	if err != nil {
		http.Error(w, "Failed to generate DKIM keys", http.StatusInternalServerError)
		return
	}
	cnameTarget := fmt.Sprintf("dkim-%s.esp.com", strings.ReplaceAll(domainID, "-", ""))

	_, err = db.Exec(`
		UPDATE domains SET dkim_private_key = $1, dkim_public_key = $2, cname_target = $3
		WHERE id = $4
	`, privPEM, pubB64, cnameTarget, domainID)
	if err != nil {
		http.Error(w, "Failed to store DKIM keys", http.StatusInternalServerError)
		return
	}

	resp := CreateDomainResponse{
		ID:         domainID,
		DomainName: domainName,
		Status:     "PENDING",
		DNSRecords: []DomainDNSRecord{
			{Type: "MX", Host: domainName, Value: mailHostname(), Priority: 10},
			{Type: "TXT", Host: domainName, Value: "v=spf1 mx ~all"},
			{Type: "TXT", Host: "esp._domainkey." + domainName, Value: "v=DKIM1; k=rsa; p=" + pubB64},
			{Type: "TXT", Host: "_dmarc." + domainName, Value: "v=DMARC1; p=quarantine; rua=mailto:postmaster@" + domainName},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleGetDomain returns one domain's status + DNS records (re-derived, read-only).
// GET /api/domains/{id}
func handleGetDomain(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	if orgID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	domainID := r.PathValue("id")

	var domainName, status, pubB64 string
	var isVerified bool
	err := db.QueryRow(`
		SELECT domain_name, status, is_verified, COALESCE(dkim_public_key, '')
		FROM domains WHERE id = $1 AND org_id = $2
	`, domainID, orgID).Scan(&domainName, &status, &isVerified, &pubB64)
	if err != nil {
		http.Error(w, "Domain not found", http.StatusNotFound)
		return
	}

	resp := map[string]interface{}{
		"id":          domainID,
		"domain_name": domainName,
		"status":      status,
		"is_verified": isVerified,
		"dns_records": []DomainDNSRecord{
			{Type: "MX", Host: domainName, Value: mailHostname(), Priority: 10},
			{Type: "TXT", Host: domainName, Value: "v=spf1 mx ~all"},
			{Type: "TXT", Host: "esp._domainkey." + domainName, Value: "v=DKIM1; k=rsa; p=" + pubB64},
			{Type: "TXT", Host: "_dmarc." + domainName, Value: "v=DMARC1; p=quarantine; rua=mailto:postmaster@" + domainName},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
