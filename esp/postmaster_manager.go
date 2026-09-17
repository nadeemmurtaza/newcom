package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Phase 43: Domain Postmaster Analytics Engine

type PostmasterManager struct {
	rdb *redis.Client
	db  *sql.DB
}

func NewPostmasterManager(rdb *redis.Client, db *sql.DB) *PostmasterManager {
	return &PostmasterManager{rdb: rdb, db: db}
}

// RecordMetric increments high-speed counters for a given domain and day
// Metrics: "sent", "bounced", "spam", "auth_failed"
func (pm *PostmasterManager) RecordMetric(ctx context.Context, domain, metric string, count int) {
	if domain == "" {
		return
	}
	// Extract domain if an email address was passed by mistake
	parts := strings.Split(domain, "@")
	if len(parts) == 2 {
		domain = parts[1]
	}

	dateStr := time.Now().UTC().Format("2006-01-02")
	key := fmt.Sprintf("postmaster:stats:%s:%s", domain, dateStr)

	// Increment the hash field
	pm.rdb.HIncrBy(ctx, key, metric, int64(count))
	
	// Retain data for 60 days
	pm.rdb.Expire(ctx, key, 60*24*time.Hour)
}

type DailyStats struct {
	Date          string  `json:"date"`
	Sent          int64   `json:"sent"`
	Bounced       int64   `json:"bounced"`
	SpamComplaints int64  `json:"spam_complaints"`
	AuthFailed    int64   `json:"auth_failed"`
}

type PostmasterReport struct {
	Domain          string       `json:"domain"`
	ReputationTier  string       `json:"reputation_tier"`
	SpamRatePercent float64      `json:"spam_rate_percent"`
	DailyStats      []DailyStats `json:"history"`
}

// GetStats returns the historical data and calculated reputation
func (pm *PostmasterManager) GetStats(ctx context.Context, domain string, days int) (*PostmasterReport, error) {
	var history []DailyStats
	var totalSent, totalSpam int64

	now := time.Now().UTC()
	for i := days - 1; i >= 0; i-- {
		dateStr := now.AddDate(0, 0, -i).Format("2006-01-02")
		key := fmt.Sprintf("postmaster:stats:%s:%s", domain, dateStr)

		res, err := pm.rdb.HGetAll(ctx, key).Result()
		if err != nil {
			continue
		}

		stats := DailyStats{Date: dateStr}
		if v, ok := res["sent"]; ok { fmt.Sscanf(v, "%d", &stats.Sent) }
		if v, ok := res["bounced"]; ok { fmt.Sscanf(v, "%d", &stats.Bounced) }
		if v, ok := res["spam"]; ok { fmt.Sscanf(v, "%d", &stats.SpamComplaints) }
		if v, ok := res["auth_failed"]; ok { fmt.Sscanf(v, "%d", &stats.AuthFailed) }

		// Aggregate for reputation (only last 7 days usually, but we'll use requested window for simplicity)
		totalSent += stats.Sent
		totalSpam += stats.SpamComplaints

		history = append(history, stats)
	}

	spamRate := 0.0
	if totalSent > 0 {
		spamRate = (float64(totalSpam) / float64(totalSent)) * 100.0
	}

	reputation := "HIGH"
	if totalSent > 100 { // Need a minimum threshold to evaluate fairly
		if spamRate > 0.3 {
			reputation = "BAD"
		} else if spamRate > 0.1 {
			reputation = "MEDIUM"
		}
	} else if totalSent == 0 {
		reputation = "UNKNOWN"
	}

	return &PostmasterReport{
		Domain:          domain,
		ReputationTier:  reputation,
		SpamRatePercent: spamRate,
		DailyStats:      history,
	}, nil
}
