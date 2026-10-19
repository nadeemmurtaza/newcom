package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Phase 41: White-Label Portal & Dynamic Branding Routing

type BrandingConfig struct {
	OrgID         string `json:"org_id"`
	DomainID      string `json:"domain_id"`
	PortalDomain  string `json:"portal_domain"`
	LogoURL       string `json:"logo_url"`
	PrimaryColor  string `json:"primary_color"`
}

type BrandingManager struct {
	db  *sql.DB
	rdb *redis.Client
}

func NewBrandingManager(db *sql.DB, rdb *redis.Client) *BrandingManager {
	return &BrandingManager{db: db, rdb: rdb}
}

// GetBrandingByHost fetches branding from Redis cache, falling back to PostgreSQL
func (bm *BrandingManager) GetBrandingByHost(ctx context.Context, host string) (*BrandingConfig, error) {
	// Strip port if present (e.g., localhost:8080 -> localhost)
	host = strings.Split(host, ":")[0]

	cacheKey := "branding:" + host
	
	// 1. Check Redis Cache (High Speed)
	cached, err := bm.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		var config BrandingConfig
		if json.Unmarshal([]byte(cached), &config) == nil {
			return &config, nil
		}
	}

	// 2. Fallback to PostgreSQL
	var config BrandingConfig
	var logo, color sql.NullString
	
	err = bm.db.QueryRow(`
		SELECT org_id, id, portal_domain, brand_logo_url, brand_primary_color
		FROM domains
		WHERE portal_domain = $1 OR domain_name = $1
	`, host).Scan(&config.OrgID, &config.DomainID, &config.PortalDomain, &logo, &color)

	if err != nil {
		return nil, err // Domain not found or not white-labeled
	}

	if logo.Valid {
		config.LogoURL = logo.String
	}
	if color.Valid {
		config.PrimaryColor = color.String
	}

	// Cache for 5 minutes
	configBytes, _ := json.Marshal(config)
	bm.rdb.Set(ctx, cacheKey, configBytes, 5*time.Minute)

	return &config, nil
}

type contextKey string
const BrandingContextKey contextKey = "brandingConfig"

// BrandingMiddleware intercepts every request, checks the Host header, and injects tenant context
func (bm *BrandingManager) BrandingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host

		// Phase 55: this used to have an empty if-block here ("skip if
		// it's the master API domain") that did nothing at all -- the
		// lookup below always ran regardless, and the check itself named
		// "ouresp.com", a domain nobody deploying this owns. Removed the
		// dead branch; GetBrandingByHost already no-ops correctly (err !=
		// nil) for a host with no white-label config.
		config, err := bm.GetBrandingByHost(r.Context(), host)
		if err == nil && config != nil {
			// Inject White-Label Context
			ctx := context.WithValue(r.Context(), BrandingContextKey, config)
			r = r.WithContext(ctx)
		}

		next.ServeHTTP(w, r)
	})
}

