package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Phase 33: Enterprise Hardening

// ==========================================
// 1. SECURITY: AES-256-GCM Encryption at Rest
// ==========================================

// Phase 50: Load the master key from the environment (ideally backed by a
// real KMS) instead of a literal in source. A hardcoded key means anyone
// with read access to this repo could decrypt everything at rest.
var masterEncryptionKey = loadMasterEncryptionKey()

func loadMasterEncryptionKey() []byte {
	key := os.Getenv("MASTER_ENCRYPTION_KEY")
	if len(key) != 32 {
		log.Fatal("MASTER_ENCRYPTION_KEY must be set to exactly 32 bytes (AES-256). Generate one with: openssl rand -base64 24 | cut -c1-32")
	}
	return []byte(key)
}

func EncryptAES(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(masterEncryptionKey)
	if err != nil {
		return nil, err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := aesGCM.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

func DecryptAES(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(masterEncryptionKey)
	if err != nil {
		return nil, err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := aesGCM.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

// ==========================================
// 2. SPEED: Redis Read-Through Cache
// ==========================================

type AuthCache struct {
	rdb *redis.Client
}

func NewAuthCache(rdb *redis.Client) *AuthCache {
	return &AuthCache{rdb: rdb}
}

// CheckCache returns the comma separated subAccountID,orgID if exists
func (ac *AuthCache) Get(ctx context.Context, apiKey string) (string, error) {
	return ac.rdb.Get(ctx, "auth_cache:"+apiKey).Result()
}

func (ac *AuthCache) Set(ctx context.Context, apiKey string, value string) {
	// Cache auth credentials for 60 seconds to relieve PostgreSQL load
	ac.rdb.Set(ctx, "auth_cache:"+apiKey, value, 60*time.Second)
}

// ==========================================
// 3. RELIABILITY: Circuit Breaker
// ==========================================

type CircuitState int

const (
	StateClosed CircuitState = iota // Flowing normally
	StateOpen                       // Tripped, blocking requests
	StateHalfOpen                   // Testing if healthy again
)

type CircuitBreaker struct {
	mu             sync.Mutex
	state          CircuitState
	failureCount   int
	failureThresh  int
	resetTimeout   time.Duration
	lastFailure    time.Time
}

func NewCircuitBreaker(threshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:         StateClosed,
		failureThresh: threshold,
		resetTimeout:  timeout,
	}
}

func (cb *CircuitBreaker) Execute(work func() error) error {
	cb.mu.Lock()
	if cb.state == StateOpen {
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			log.Println("Circuit Breaker: Entering Half-Open state...")
			cb.state = StateHalfOpen
		} else {
			cb.mu.Unlock()
			return errors.New("CIRCUIT BREAKER OPEN: Fast failing request to protect downstream service")
		}
	}
	cb.mu.Unlock()

	err := work()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failureCount++
		cb.lastFailure = time.Now()
		if cb.failureCount >= cb.failureThresh {
			if cb.state != StateOpen {
				log.Println("🚨 CIRCUIT BREAKER TRIPPED! Stopping traffic to downstream service.")
				cb.state = StateOpen
			}
		}
		return err
	}

	// Success
	if cb.state == StateHalfOpen {
		log.Println("Circuit Breaker: Service recovered. Closing circuit.")
	}
	cb.state = StateClosed
	cb.failureCount = 0
	return nil
}

// ==========================================
// Phase 34: ULTIMATE RESILIENCE
// ==========================================

// 4. RATE LIMITING (Distributed Sliding Window / Counter)
type RateLimiter struct {
	rdb *redis.Client
}

func NewRateLimiter(rdb *redis.Client) *RateLimiter {
	return &RateLimiter{rdb: rdb}
}

func (rl *RateLimiter) Allow(ctx context.Context, identifier string, limit int64, window time.Duration) bool {
	key := "rate_limit:" + identifier
	// Using a simple atomic increment with TTL
	count, err := rl.rdb.Incr(ctx, key).Result()
	if err != nil {
		return true // Fail open
	}
	if count == 1 {
		rl.rdb.Expire(ctx, key, window)
	}
	
	if count > limit {
		return false // Rate limit exceeded
	}
	return true
}

// 5. IDEMPOTENCY ENGINE (Prevent Double Execution)
type IdempotencyEngine struct {
	rdb *redis.Client
}

func NewIdempotencyEngine(rdb *redis.Client) *IdempotencyEngine {
	return &IdempotencyEngine{rdb: rdb}
}

func (ie *IdempotencyEngine) Check(ctx context.Context, idempotencyKey string) (string, bool) {
	if idempotencyKey == "" {
		return "", false
	}
	val, err := ie.rdb.Get(ctx, "idempotency:"+idempotencyKey).Result()
	if err == nil && val != "" {
		return val, true // Exists, return cached response
	}
	return "", false
}

func (ie *IdempotencyEngine) Save(ctx context.Context, idempotencyKey string, responseJSON string) {
	if idempotencyKey == "" { return }
	// Cache the response for 24 hours
	ie.rdb.Set(ctx, "idempotency:"+idempotencyKey, responseJSON, 24*time.Hour)
}

// 6. DEAD LETTER QUEUE (DLQ) ROUTING
func RouteToDLQ(ctx context.Context, rdb *redis.Client, queueName string, payload string, reason string) {
	log.Printf("☠️ DLQ ROUTING: Moving poisoned payload from %s to DLQ. Reason: %s", queueName, reason)
	
	dlqPayload := map[string]interface{}{
		"original_queue": queueName,
		"failed_at": time.Now().Format(time.RFC3339),
		"reason": reason,
		"raw_payload": payload,
	}
	dlqBytes, _ := json.Marshal(dlqPayload)
	rdb.LPush(ctx, "dlq_global", dlqBytes)
}
