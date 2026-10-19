package main

import (
	"context"
	"database/sql"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// Phase 38: Continuous DNS Verification Worker
func startContinuousDNSWorker(ctx context.Context, wg *sync.WaitGroup, db *sql.DB) {
	defer wg.Done()
	log.Println("Continuous DNS Verification Worker Started")

	// For simulation, we'll check every 30 seconds. In reality, every 24 hours.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Continuous DNS Verification Worker shutting down...")
			return
		case <-ticker.C:
			// Fetch all verified domains
			rows, err := db.Query("SELECT id, domain_name, COALESCE(cname_target, '') FROM domains WHERE status = 'VERIFIED'")
			if err != nil {
				continue
			}

			type domainCheck struct {
				ID          string
				DomainName  string
				CnameTarget string
			}
			
			var toCheck []domainCheck
			for rows.Next() {
				var dc domainCheck
				if err := rows.Scan(&dc.ID, &dc.DomainName, &dc.CnameTarget); err == nil {
					toCheck = append(toCheck, dc)
				}
			}
			rows.Close()

			for _, dc := range toCheck {
				// 1. Verify SPF -- same real check as handleDomainVerify
				// (dns_checker.go): require the "mx"/"a" mechanism or an
				// explicit include of our hostname, not just any SPF record.
				spfVerified := false
				txtRecords, _ := net.LookupTXT(dc.DomainName)
				for _, txt := range txtRecords {
					if !strings.HasPrefix(txt, "v=spf1") {
						continue
					}
					for _, mechanism := range strings.Fields(txt) {
						if mechanism == "mx" || mechanism == "a" || mechanism == "include:"+mailHostname() {
							spfVerified = true
							break
						}
					}
					if spfVerified {
						break
					}
				}

				// 2. Verify DKIM (CNAME)
				dkimVerified := false
				if dc.CnameTarget != "" {
					cname, _ := net.LookupCNAME("esp._domainkey." + dc.DomainName)
					if cname != "" && strings.Contains(cname, dc.CnameTarget) {
						dkimVerified = true
					}
				}

				// 3. Verify DMARC
				dmarcVerified := false
				dmarcRecords, _ := net.LookupTXT("_dmarc." + dc.DomainName)
				for _, txt := range dmarcRecords {
					if strings.HasPrefix(txt, "v=DMARC1") {
						dmarcVerified = true
						break
					}
				}

				if !spfVerified || !dkimVerified || !dmarcVerified {
					log.Printf("ALERT: Domain %s failed continuous DNS check. Downgrading to PENDING.", dc.DomainName)
					
					db.Exec(`
						UPDATE domains 
						SET is_verified = false, status = 'PENDING'
						WHERE id = $1
					`, dc.ID)
					
					// Trigger an alert to the organization...
				}
			}
		}
	}
}
