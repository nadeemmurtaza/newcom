package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
