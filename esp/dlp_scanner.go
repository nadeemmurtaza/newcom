package main

import (
	"database/sql"
	"log"
	"regexp"
	"strings"
)

func passesLuhn(cc string) bool {
	cc = strings.ReplaceAll(cc, "-", "")
	cc = strings.ReplaceAll(cc, " ", "")
	sum := 0
	alt := false
	for i := len(cc) - 1; i >= 0; i-- {
		n := int(cc[i] - '0')
		if alt {
			n *= 2
			if n > 9 {
				n = (n % 10) + 1
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0
}

// CheckDLP scans the email body against the organization's Data Loss Prevention policies
func CheckDLP(db *sql.DB, orgID string, body string) (bool, string, error) {
	// Deep Logic: System-wide PII / PCI compliance checks
	ssnRegex := regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	ccRegex := regexp.MustCompile(`\b(?:\d[ -]*?){13,16}\b`)
	
	if ssnRegex.MatchString(body) {
		return true, "Blocked by System DLP: High-Risk PII (SSN) detected", nil
	}

	for _, match := range ccRegex.FindAllString(body, -1) {
		if passesLuhn(match) {
			return true, "Blocked by System DLP: High-Risk PCI (Credit Card) detected", nil
		}
	}

	// Cloud API keys (basic entropy match proxy)
	apiKeyRegex := regexp.MustCompile(`(?i)(?:api_key|secret|token)["']?\s*[:=]\s*["']?[A-Za-z0-9_-]{32,}["']?`)
	if apiKeyRegex.MatchString(body) {
		return true, "Blocked by System DLP: Cloud API Key / Secret detected", nil
	}

	// Custom org policies
	rows, err := db.Query("SELECT regex_pattern, action, name FROM dlp_policies WHERE org_id = $1", orgID)
	if err != nil {
		return false, "", err
	}
	defer rows.Close()

	for rows.Next() {
		var pattern, action, name string
		if err := rows.Scan(&pattern, &action, &name); err != nil {
			log.Printf("DLP read error: %v", err)
			continue
		}

		re, err := regexp.Compile(pattern)
		if err != nil {
			log.Printf("Invalid DLP regex '%s' in policy %s: %v", pattern, name, err)
			continue
		}

		if re.MatchString(body) {
			actionStr := strings.ToLower(action)
			log.Printf("DLP Policy %s matched. Action: %s", name, actionStr)
			
			if actionStr == "block" || actionStr == "quarantine" {
				return true, "Blocked by DLP Policy: " + name, nil
			}
		}
	}
	return false, "", nil
}
