package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// Phase 48: Advanced Anti-Abuse (Machine Learning Hook)

type MLAbuseScanner struct {
	db         *sql.DB
	httpClient *http.Client
}

func NewMLAbuseScanner(db *sql.DB) *MLAbuseScanner {
	// Strict 50ms timeout so the ML inference never bottlenecks the Go MTA pipeline
	client := &http.Client{
		Timeout: 50 * time.Millisecond,
	}
	return &MLAbuseScanner{
		db:         db,
		httpClient: client,
	}
}

// ScanContent simulates sending the payload to a Python ML microservice via HTTP
func (ml *MLAbuseScanner) ScanContent(orgID, subject, body string) float64 {
	// In a production system, we would construct the request and send it:
	payload := map[string]string{
		"org_id":  orgID,
		"subject": subject,
		"body":    body,
	}
	payloadBytes, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", "http://ml-anti-abuse-service.internal:50051/v1/predict", bytes.NewBuffer(payloadBytes))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		// ml.httpClient.Do(req) // Commented out to avoid actual network failure in simulation
	}

	// ==========================================
	// SIMULATED ML INFERENCE RESPONSE
	// ==========================================
	// We simulate the neural network analyzing text semantics.
	// If the text contains blatant crypto phishing keywords, it scores highly.
	lowerBody := strings.ToLower(body)
	if strings.Contains(lowerBody, "bitcoin") || strings.Contains(lowerBody, "verify your wallet") {
		// Simulate a high probability of phishing
		return 0.90 + (rand.Float64() * 0.09) // 0.90 - 0.99
	}
	
	if strings.Contains(lowerBody, "urgent action required") {
		return 0.70 + (rand.Float64() * 0.15)
	}

	// Normal clean emails have a very low score
	return rand.Float64() * 0.10 // 0.00 - 0.10
}
