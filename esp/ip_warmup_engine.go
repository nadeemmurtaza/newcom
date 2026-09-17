package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Phase 46: IP Warmup & Dedicated IP Lifecycle State Machine

// Warmup Schedule (Emails per day)
var warmupSchedule = []int{
	0,        // Day 0 (placeholder for 1-based indexing)
	50,      // Day 1
	100,     // Day 2
	500,     // Day 3
	1000,    // Day 4
	5000,    // Day 5
	10000,   // Day 6
	50000,   // Day 7
	100000,  // Day 8
	500000,  // Day 9
	1000000, // Day 10 (Fully warmed up)
}

func startIPWarmupCron(ctx context.Context, wg *sync.WaitGroup, db *sql.DB) {
	defer wg.Done()
	log.Println("IP Warmup Engine: Cron started")

	// Run every 24 hours in reality, simulate every minute
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			advanceWarmupSchedules(db)
		}
	}
}

func advanceWarmupSchedules(db *sql.DB) {
	log.Println("[IP Warmup] Evaluating Dedicated IPs for daily progression...")

	rows, err := db.Query(`
		SELECT id, ip_address, warmup_day, status 
		FROM dedicated_ips 
		WHERE status = 'WARMING_UP'
	`)
	if err != nil {
		log.Printf("[IP Warmup] Query error: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, ip, status string
		var currentDay int
		if err := rows.Scan(&id, &ip, &currentDay, &status); err != nil {
			continue
		}

		nextDay := currentDay + 1
		if nextDay >= len(warmupSchedule) {
			// Fully Warmed Up
			db.Exec("UPDATE dedicated_ips SET status = 'ACTIVE', warmup_day = $1, current_daily_limit = $2, updated_at = NOW() WHERE id = $3", nextDay, warmupSchedule[len(warmupSchedule)-1], id)
			log.Printf("[IP Warmup] %s is now fully ACTIVE.", ip)
		} else {
			limit := warmupSchedule[nextDay]
			db.Exec("UPDATE dedicated_ips SET warmup_day = $1, current_daily_limit = $2, updated_at = NOW() WHERE id = $3", nextDay, limit, id)
			log.Printf("[IP Warmup] %s advanced to Day %d (Limit: %d).", ip, nextDay, limit)
		}
	}
}

// GetDedicatedIPForDispatch checks if the org has a dedicated IP and if it hasn't exceeded its warmup limit for the day.
// If it exceeds, it returns "", forcing the caller to fallback to the Shared Pool (Spillover logic).
func GetDedicatedIPForDispatch(ctx context.Context, db *sql.DB, rdb *redis.Client, orgID string) string {
	var ipAddress string
	var status string
	var currentLimit int

	err := db.QueryRow(`
		SELECT ip_address, status, current_daily_limit 
		FROM dedicated_ips 
		WHERE org_id = $1 AND status IN ('WARMING_UP', 'ACTIVE')
	`, orgID).Scan(&ipAddress, &status, &currentLimit)

	if err != nil {
		return "" // No dedicated IP found or error
	}

	dateStr := time.Now().UTC().Format("2006-01-02")
	redisKey := fmt.Sprintf("ip_warmup_sent:%s:%s", ipAddress, dateStr)

	// Increment and get the count atomically
	sentToday, err := rdb.Incr(ctx, redisKey).Result()
	if err != nil {
		return "" 
	}
	// Expire the key after 48h to prevent Redis memory leak
	if sentToday == 1 {
		rdb.Expire(ctx, redisKey, 48*time.Hour)
	}

	if status == "WARMING_UP" && int(sentToday) > currentLimit {
		log.Printf("[IP Spillover] Dedicated IP %s reached daily warmup limit (%d). Routing to Shared Pool.", ipAddress, currentLimit)
		// Undo the increment so we don't skew stats
		rdb.Decr(ctx, redisKey)
		return "" 
	}

	return ipAddress
}
