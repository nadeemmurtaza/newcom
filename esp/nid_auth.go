package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"
)

// Phase 57: "Login with nID" (nadeemmurtaza/nID) -- the one identity above
// everything else in this file. nmail is a relying party of nID exactly
// the way nID's own README describes: a redirect carries a one-time code,
// nmail's backend exchanges it server-side with its client secret for the
// nID Account's identity claims. One nID Account <-> one nmail users row
// (users.nid_account_id, UNIQUE) <-> that person's one personal mailbox
// plus every work mailbox they've been assigned, across any org -- see
// GET /api/me/mailboxes.

func nidBaseURL() string {
	if v := os.Getenv("NID_BASE_URL"); v != "" {
		return v
	}
	return "http://localhost:4000"
}

func nidClientID() string     { return os.Getenv("NID_CLIENT_ID") }
func nidClientSecret() string { return os.Getenv("NID_CLIENT_SECRET") }

func nidRedirectURI() string {
	if v := os.Getenv("NID_REDIRECT_URI"); v != "" {
		return v
	}
	return "http://localhost:8081/api/users/login/nid/callback"
}

const nidLoginStateTTL = 10 * time.Minute

// handleNidLoginStart begins a "Login with nID" attempt: it asks nID to
// open a login request and hands back the URL the person's browser should
// go to (apps/login-ui's /authorize/:pollToken, either visited directly or
// shown as a QR code for a cross-device scan -- nID doesn't distinguish
// the two, see its oauth.service.ts).
// GET /api/users/login/nid/start
func handleNidLoginStart(w http.ResponseWriter, r *http.Request) {
	if nidClientID() == "" || nidClientSecret() == "" {
		http.Error(w, "nID login is not configured (NID_CLIENT_ID/NID_CLIENT_SECRET unset)", http.StatusServiceUnavailable)
		return
	}

	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		http.Error(w, "Failed to start login", http.StatusInternalServerError)
		return
	}
	state := hex.EncodeToString(stateBytes)

	reqBody, _ := json.Marshal(map[string]string{
		"clientId":    nidClientID(),
		"redirectUri": nidRedirectURI(),
		"state":       state,
	})
	resp, err := http.Post(nidBaseURL()+"/v1/oauth/login-requests", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		http.Error(w, "nID is unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		http.Error(w, "nID rejected the login request: "+string(body), http.StatusBadGateway)
		return
	}

	var created struct {
		PollToken string `json:"pollToken"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		http.Error(w, "Unexpected response from nID", http.StatusBadGateway)
		return
	}

	// Remember this state belongs to this poll token, so the callback can
	// reject a code/state pair that didn't originate from a request this
	// server actually started (basic CSRF protection on the redirect).
	if err := rdb.Set(r.Context(), "nid_login_state:"+state, created.PollToken, nidLoginStateTTL).Err(); err != nil {
		http.Error(w, "Failed to start login", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"authorize_url": nidBaseURL() + "/authorize/" + created.PollToken,
		"poll_token":    created.PollToken,
		"state":         state,
	})
}

// handleNidLoginCallback is where nID's login-ui redirects the browser
// after a person approves the login on their nID account (phone + OTP).
// It exchanges the one-time code server-to-server for the real Account
// identity, finds or creates the matching nmail user, and issues a normal
// nmail session (the same one handleUserLogin issues) -- from here on,
// nID-authenticated and password-authenticated nmail sessions work
// identically against userAuthMiddleware.
// GET /api/users/login/nid/callback?code=...&state=...
func handleNidLoginCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Error(w, "Missing code or state", http.StatusBadRequest)
		return
	}

	if _, err := rdb.Get(r.Context(), "nid_login_state:"+state).Result(); err != nil {
		http.Error(w, "Unrecognized or expired login attempt", http.StatusBadRequest)
		return
	}
	rdb.Del(r.Context(), "nid_login_state:"+state)

	reqBody, _ := json.Marshal(map[string]string{
		"code":         code,
		"clientId":     nidClientID(),
		"clientSecret": nidClientSecret(),
		"redirectUri":  nidRedirectURI(),
	})
	resp, err := http.Post(nidBaseURL()+"/v1/oauth/token", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		http.Error(w, "nID is unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		http.Error(w, "nID rejected the code: "+string(body), http.StatusUnauthorized)
		return
	}

	var exchange struct {
		Account struct {
			ID              string  `json:"id"`
			Phone           string  `json:"phone"`
			Email           *string `json:"email"`
			PhoneVerifiedAt *string `json:"phoneVerifiedAt"`
		} `json:"account"`
	}
	if err := json.Unmarshal(body, &exchange); err != nil || exchange.Account.ID == "" {
		http.Error(w, "Unexpected response from nID", http.StatusBadGateway)
		return
	}

	fallbackEmail := exchange.Account.Email
	var fallback string
	if fallbackEmail != nil && *fallbackEmail != "" {
		fallback = *fallbackEmail
	} else {
		fallback = exchange.Account.Phone + "@nid.local" // placeholder identity string, never used for actual mail delivery
	}

	userID, err := findOrCreateUserByNidAccount(exchange.Account.ID, fallback)
	if err != nil {
		http.Error(w, "Failed to resolve account: "+err.Error(), http.StatusInternalServerError)
		return
	}

	token, err := issueUserSession(r.Context(), userID)
	if err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"token":          token,
		"user_id":        userID,
		"nid_account_id": exchange.Account.ID,
		"expires_in":     int(userSessionTTL.Seconds()),
	})
}

// findOrCreateUserByNidAccount resolves an nID Account ID to this nmail
// deployment's one matching users row, creating it on first contact.
// Enforced unique by users.nid_account_id -- there is no path in this
// codebase that lets two nmail users rows share one nID identity.
//
// Also used by handleCreateMailbox's optional assignee_nid_account_id: a
// domain owner can assign a work mailbox to a coworker's nID even if that
// person has never logged into nmail before -- the row is created here,
// exactly as it would be on their own first "Login with nID".
func findOrCreateUserByNidAccount(nidAccountID, fallbackEmail string) (string, error) {
	var userID string
	err := db.QueryRow("SELECT id FROM users WHERE nid_account_id = $1", nidAccountID).Scan(&userID)
	if err == nil {
		return userID, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}

	err = db.QueryRow(
		"INSERT INTO users (email, nid_account_id) VALUES ($1, $2) RETURNING id",
		fallbackEmail, nidAccountID,
	).Scan(&userID)
	if err != nil {
		return "", err
	}
	return userID, nil
}

// issueUserSession mints the same Redis-backed session token
// handleUserLogin issues, for a userID already known to belong to this
// request (nID-verified or otherwise) -- extracted so both login paths
// produce an identical session a caller can't tell apart.
func issueUserSession(ctx context.Context, userID string) (string, error) {
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		return "", err
	}
	token := hex.EncodeToString(rawToken)
	if err := rdb.Set(ctx, "user_session:"+token, userID, userSessionTTL).Err(); err != nil {
		return "", err
	}
	return token, nil
}
