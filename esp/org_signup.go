package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

// Phase 62: Self-Service Organization & API Key Onboarding
//
// Before this, an organization could only ever be created by a direct DB
// insert (ensurePersonalDomain's platform org was the one exception, and
// that's internal) -- nothing let a real person sign up to send mail or
// manage domains of their own. Same gap one level down: api_keys had a
// column and a consumer (authMiddleware) but no endpoint ever inserted a
// row into it, so even a hand-created org had no way to actually get a
// key. These three endpoints close that loop end to end: create an org,
// see which orgs you belong to, mint that org's first API key -- all
// gated by the same verified session (userAuthMiddleware) everything
// else under /api/me/ uses.

// orgOwnerPermissions is every permission string this codebase checks via
// hasPermission, granted in full to whoever creates an org. Kept in one
// place so a new permission string introduced elsewhere doesn't quietly
// lock the org's own creator out of using it.
var orgOwnerPermissions = []string{
	"domains:read", "domains:write",
	"analytics:read",
	"mail:read",
	"webhooks:read", "webhooks:write",
	"templates:read", "templates:write",
	"drive:read", "drive:write",
	"calendar:write",
	"security:admin",
	"keys:write",
}

// handleCreateOrg creates a new organization owned by the calling user,
// who is granted every permission this codebase checks within it.
// POST /api/orgs  body: {"name": "Acme Inc"}
func handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID") // set by userAuthMiddleware from a verified session

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var orgID string
	if err := tx.QueryRow("INSERT INTO organizations (name) VALUES ($1) RETURNING id", name).Scan(&orgID); err != nil {
		http.Error(w, "Failed to create organization", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec("INSERT INTO organization_users (org_id, user_id) VALUES ($1, $2)", orgID, userID); err != nil {
		http.Error(w, "Failed to add you to the organization", http.StatusInternalServerError)
		return
	}
	for _, perm := range orgOwnerPermissions {
		if _, err := tx.Exec(
			"INSERT INTO organization_user_permissions (org_id, user_id, permission) VALUES ($1, $2, $3)",
			orgID, userID, perm,
		); err != nil {
			http.Error(w, "Failed to grant permissions", http.StatusInternalServerError)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": orgID, "name": name})
}

// handleMyOrgs lists the organizations the calling user belongs to.
// GET /api/me/orgs
func handleMyOrgs(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID") // set by userAuthMiddleware from a verified session

	rows, err := db.Query(`
		SELECT o.id, o.name, o.tier
		FROM organizations o
		JOIN organization_users ou ON ou.org_id = o.id
		WHERE ou.user_id = $1
		ORDER BY o.created_at
	`, userID)
	if err != nil {
		http.Error(w, "Failed to query organizations", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type orgSummary struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Tier string `json:"tier"`
	}
	var out []orgSummary
	for rows.Next() {
		var o orgSummary
		if err := rows.Scan(&o.ID, &o.Name, &o.Tier); err == nil {
			out = append(out, o)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// handleCreateApiKey mints a new API key for an org the caller has
// keys:write in. The key is tied to a fresh sub_account (the identity
// authMiddleware and every /v1 endpoint resolve back to) and shown once.
// POST /api/me/orgs/{orgId}/api-keys  body: {"name": "Production"}
func handleCreateApiKey(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID") // set by userAuthMiddleware from a verified session
	orgID := r.PathValue("orgId")

	if !hasPermission(userID, orgID, "keys:write") {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Default"
	}

	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var subAccountID string
	if err := tx.QueryRow(
		"INSERT INTO sub_accounts (org_id, account_name) VALUES ($1, $2) RETURNING id",
		orgID, name,
	).Scan(&subAccountID); err != nil {
		http.Error(w, "Failed to create sub-account", http.StatusInternalServerError)
		return
	}

	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		http.Error(w, "Failed to generate key", http.StatusInternalServerError)
		return
	}
	key := "nmail_" + hex.EncodeToString(rawKey)

	var keyID string
	if err := tx.QueryRow(
		"INSERT INTO api_keys (sub_account_id, key_hash, name) VALUES ($1, $2, $3) RETURNING id",
		subAccountID, key, name,
	).Scan(&keyID); err != nil {
		http.Error(w, "Failed to create API key", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"id":   keyID,
		"key":  key, // shown once; only this response ever carries it
		"name": name,
	})
}

// handleListApiKeys lists an org's API keys (names/metadata only, never
// the key value -- that was only ever shown once, at creation).
// GET /api/me/orgs/{orgId}/api-keys
func handleListApiKeys(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID") // set by userAuthMiddleware from a verified session
	orgID := r.PathValue("orgId")

	if !hasPermission(userID, orgID, "keys:write") {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	rows, err := db.Query(`
		SELECT a.id, a.name, a.created_at, a.last_used_at
		FROM api_keys a
		JOIN sub_accounts s ON a.sub_account_id = s.id
		WHERE s.org_id = $1
		ORDER BY a.created_at
	`, orgID)
	if err != nil {
		http.Error(w, "Failed to query API keys", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type keySummary struct {
		ID         string  `json:"id"`
		Name       string  `json:"name"`
		CreatedAt  string  `json:"created_at"`
		LastUsedAt *string `json:"last_used_at"`
	}
	var out []keySummary
	for rows.Next() {
		var k keySummary
		if err := rows.Scan(&k.ID, &k.Name, &k.CreatedAt, &k.LastUsedAt); err == nil {
			out = append(out, k)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
