package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// Phase 36: Enterprise Multi-Tenancy (Quotas & Data Residency)

type TenantManager struct {
	db  *sql.DB
	rdb *redis.Client
}

func NewTenantManager(db *sql.DB, rdb *redis.Client) *TenantManager {
	return &TenantManager{db: db, rdb: rdb}
}

// EnsureRegion tests if the API hit the correct geographic cluster
func (tm *TenantManager) EnsureRegion(expectedRegion string, currentRegion string) bool {
	// In a real load balancer, currentRegion might be derived from os.Getenv("REGION")
	// If a European customer hits US-EAST, we block or proxy.
	return expectedRegion == currentRegion
}

// CheckQuota enforces the Noisy Neighbor rate limits using a fast Redis atomic incrementer
func (tm *TenantManager) CheckQuota(ctx context.Context, orgID string, quota int) bool {
	// E.g., tenant_quota:org123:2026-09
	key := fmt.Sprintf("tenant_quota:%s:%s", orgID, time.Now().Format("2006-01"))

	count, err := tm.rdb.Incr(ctx, key).Result()
	if err != nil {
		log.Printf("TenantManager: Redis error on quota check %v", err)
		return true // Fail open
	}

	if count == 1 {
		// Expire at the end of the month
		tm.rdb.Expire(ctx, key, 31*24*time.Hour)
	}

	if int(count) > quota {
		log.Printf("TenantManager: Org %s exceeded quota of %d", orgID, quota)
		return false
	}
	return true
}
