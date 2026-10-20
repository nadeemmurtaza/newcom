package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Phase 55: Human User Session Auth
//
// dashboard.go, templates.go, webhooks.go and domains_api.go all read
// "X-User-ID" straight off the request and used it as-is for
// hasPermission(userID, orgID, ...) checks -- the header was never
// verified against anything, so any caller could claim to be any user ID
// and inherit whatever permissions organization_user_permissions granted
// that ID in any org. This is the real login + session layer: users
// (table already existed with email/password_hash, but nothing issued or
// checked a session from it) log in once, get a token, and
// userAuthMiddleware resolves that token to a real user ID -- overwriting
// whatever the client put in X-User-ID rather than trusting it.

const userSessionTTL = 24 * time.Hour

// handleUserSignup creates a new local-password identity and logs it in.
//
// Phase 62: handleUserLogin checked password_hash against a users row, but
// nothing anywhere ever inserted one -- findOrCreateUserByNidAccount only
// ever sets email+nid_account_id (nID-authenticated users have no local
// password at all, per schema.sql's comment on password_hash). Password
// login was unreachable for every person on the platform: there was no
// way to become a user at all without either nID or a hand-run INSERT.
// Open signup (anyone, like Gmail) was the chosen model for this
// platform; nID remains the stronger, preferred identity -- this is the
// fallback for someone who doesn't have one.
// POST /api/users/signup  body: {"email": "...", "password": "..."}
func handleUserSignup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || len(req.Password) < 8 {
		http.Error(w, "A valid email and a password of at least 8 characters are required", http.StatusBadRequest)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Failed to secure password", http.StatusInternalServerError)
		return
	}

	var userID string
	err = db.QueryRow("INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id", email, string(hash)).Scan(&userID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			http.Error(w, "An account with that email already exists", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create account", http.StatusInternalServerError)
		return
	}

	token, err := issueUserSession(r.Context(), userID)
	if err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"token":      token,
		"user_id":    userID,
		"expires_in": int(userSessionTTL.Seconds()),
	})
}

// handleUserLogin exchanges an email + password for a session token.
// POST /api/users/login  body: {"email": "...", "password": "..."}
func handleUserLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var userID, hash string
	err := db.QueryRow("SELECT id, password_hash FROM users WHERE email = $1", strings.ToLower(req.Email)).Scan(&userID, &hash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}
	token := hex.EncodeToString(rawToken)

	if err := rdb.Set(r.Context(), "user_session:"+token, userID, userSessionTTL).Err(); err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"token":      token,
		"user_id":    userID,
		"expires_in": int(userSessionTTL.Seconds()),
	})
}

// Phase 56: Personal Email Signup
//
// Work mailboxes are created only by whoever owns/verified the domain
// (handleCreateMailbox, org_id-gated). Personal mailboxes are the other
// half of the ask: a person signs up for their own single mailbox on the
// platform's own domain (personalEmailDomain(), the nmail equivalent of
// gmail.com) -- nobody else can create one on their behalf, and the
// database enforces at most one per person via the partial unique index
// on mailboxes(owner_user_id) WHERE kind = 'personal'.

// ensurePersonalDomain makes sure the platform's personal-signup domain
// exists and is marked as such, creating a platform organization to own it
// if neither exists yet. Idempotent -- safe to call on every signup.
func ensurePersonalDomain() (domainID, orgID string, err error) {
	const platformOrgName = "__nmail_platform__"

	err = db.QueryRow("SELECT id FROM organizations WHERE name = $1", platformOrgName).Scan(&orgID)
	if err == sql.ErrNoRows {
		err = db.QueryRow("INSERT INTO organizations (name) VALUES ($1) RETURNING id", platformOrgName).Scan(&orgID)
	}
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve platform org: %w", err)
	}

	domainName := personalEmailDomain()
	err = db.QueryRow("SELECT id FROM domains WHERE domain_name = $1", domainName).Scan(&domainID)
	if err == sql.ErrNoRows {
		// This domain is the platform's own -- we don't verify it by DNS
		// lookup like a customer domain (handleDomainVerify); we assert it
		// verified because we control it directly, the same way Google
		// doesn't run an SPF lookup against gmail.com before trusting it.
		err = db.QueryRow(`
			INSERT INTO domains (org_id, domain_name, status, is_verified, is_personal_domain)
			VALUES ($1, $2, 'VERIFIED', true, true)
			RETURNING id
		`, orgID, domainName).Scan(&domainID)
	}
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve personal domain: %w", err)
	}
	return domainID, orgID, nil
}

// handleCreatePersonalMailbox creates the calling person's one personal
// mailbox on the platform domain.
//
// Phase 57: this used to be handleUserSignup, a standalone endpoint that
// minted its own local password-based identity bundled with the mailbox
// -- exactly the identity duplication the nID integration above exists to
// prevent. A person's identity is now their nID Account (nid_auth.go)
// full stop; this only ever runs after that's already established
// (userAuthMiddleware), and only adds the one thing nID itself doesn't
// have any notion of: a personal mailbox. "At most one per person" is
// still enforced by the same partial unique index on
// mailboxes(owner_user_id) WHERE kind = 'personal' -- now keyed off a
// real nID-verified owner_user_id instead of a freshly-minted local one.
// POST /api/me/personal-mailbox  body: {"local_part": "jane"}
func handleCreatePersonalMailbox(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID") // set by userAuthMiddleware from a verified session (nID or legacy password)

	var req struct {
		LocalPart string `json:"local_part"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	localPart := strings.ToLower(strings.TrimSpace(req.LocalPart))
	if !localPartPattern.MatchString(localPart) {
		http.Error(w, "Invalid local_part", http.StatusBadRequest)
		return
	}

	_, orgID, err := ensurePersonalDomain()
	if err != nil {
		http.Error(w, "Failed to provision personal domain: "+err.Error(), http.StatusInternalServerError)
		return
	}
	emailAddress := localPart + "@" + personalEmailDomain()

	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var mailboxID string
	err = tx.QueryRow(`
		INSERT INTO mailboxes (org_id, email_address, kind, owner_user_id)
		VALUES ($1, $2, 'personal', $3)
		RETURNING id
	`, orgID, emailAddress, userID).Scan(&mailboxID)
	if err != nil {
		if strings.Contains(err.Error(), "one_personal_mailbox_per_user") {
			http.Error(w, "You already have a personal mailbox", http.StatusConflict)
			return
		}
		if strings.Contains(err.Error(), "duplicate key") {
			http.Error(w, "That email is already taken", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create mailbox: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, name := range defaultSystemFolders {
		if _, err := tx.Exec(`
			INSERT INTO folders (mailbox_id, name, is_system) VALUES ($1, $2, true)
			ON CONFLICT (mailbox_id, name) DO NOTHING
		`, mailboxID, name); err != nil {
			http.Error(w, "Failed to provision folders: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Personal mailboxes log in through the same app-password/session model
	// as work mailboxes (mailbox_auth.go's handleMailboxLogin) -- issue one
	// initial credential the same way handleCreateMailbox does.
	rawToken := make([]byte, 20)
	if _, err := rand.Read(rawToken); err != nil {
		http.Error(w, "Failed to generate credentials", http.StatusInternalServerError)
		return
	}
	plainAppPassword := hex.EncodeToString(rawToken)
	appPasswordHash, err := bcrypt.GenerateFromPassword([]byte(plainAppPassword), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Failed to secure credentials", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`
		INSERT INTO app_passwords (mailbox_id, token_hash, device_name) VALUES ($1, $2, 'initial')
	`, mailboxID, string(appPasswordHash)); err != nil {
		http.Error(w, "Failed to store credentials", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"user_id":       userID,
		"mailbox_id":    mailboxID,
		"email_address": emailAddress,
		"app_password":  plainAppPassword, // shown once; only the bcrypt hash is stored
	})
}

// userAuthMiddleware resolves "Authorization: Bearer <token>" to a real
// user ID via Redis and sets X-User-ID to it, discarding whatever the
// client put there itself.
func userAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "Unauthorized: Missing session token", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")

		userID, err := rdb.Get(r.Context(), "user_session:"+token).Result()
		if err != nil || userID == "" {
			http.Error(w, "Unauthorized: Invalid or expired session", http.StatusUnauthorized)
			return
		}

		r.Header.Set("X-User-ID", userID)
		next.ServeHTTP(w, r)
	}
}
