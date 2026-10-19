package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log"
	"net"
	"net/http"
	"strings"
	"fmt"
)

// generateDKIMKeys creates a new 2048-bit RSA keypair for DKIM signing
func generateDKIMKeys() (privateKeyPEM string, publicKeyPEM string, err error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}

	// Encode Private Key
	privDER := x509.MarshalPKCS1PrivateKey(privateKey)
	privBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privDER,
	}
	privateKeyPEM = string(pem.EncodeToMemory(privBlock))

	// Encode Public Key
	pubDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}
	pubBlock := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	}
	// DKIM DNS records usually just want the base64 part, stripping the PEM headers for storage
	publicKeyPEM = string(pem.EncodeToMemory(pubBlock))
	// Clean up PEM to just base64 string for DNS
	publicKeyPEM = strings.ReplaceAll(publicKeyPEM, "-----BEGIN PUBLIC KEY-----\n", "")
	publicKeyPEM = strings.ReplaceAll(publicKeyPEM, "\n-----END PUBLIC KEY-----\n", "")
	publicKeyPEM = strings.ReplaceAll(publicKeyPEM, "\n", "")

	return privateKeyPEM, publicKeyPEM, nil
}

// handleDomainSetup generates DKIM keys for a domain
func handleDomainSetup(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	domainID := parts[3] // /api/domains/{id}/setup
	
	// Phase 35: Enterprise Team Management - MFA Enforcement for Sensitive Endpoints
	subAccountID := r.Header.Get("X-SubAccount-ID")
	if !globalRulesEngine.EvaluateMFA(subAccountID) {
		http.Error(w, "MFA Required: You must enable MFA to manage domain cryptography.", http.StatusForbidden)
		return
	}

	privPEM, pubPEM, err := generateDKIMKeys()
	if err != nil {
		http.Error(w, "Failed to generate keys", http.StatusInternalServerError)
		return
	}

	// Phase 38: CNAME White-Labeling Target Generation
	cnameTarget := fmt.Sprintf("dkim-%s.esp.com", strings.Replace(domainID, "-", "", -1))

	_, err = db.Exec(`
		UPDATE domains 
		SET dkim_private_key = $1, dkim_public_key = $2, cname_target = $3
		WHERE id = $4
	`, privPEM, pubPEM, cnameTarget, domainID)

	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"message":      "DKIM Keys generated successfully",
		"instructions": "Create a CNAME record pointing your host to our target",
		"cname_host":   "esp._domainkey." + domainID,
		"cname_target": cnameTarget,
	})
}

// handleDomainVerify queries public DNS to see if the user configured their TXT records correctly
func handleDomainVerify(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	domainID := parts[3] // /api/domains/{id}/verify
	
	// Phase 35: Enterprise Team Management - MFA Enforcement for Sensitive Endpoints
	subAccountID := r.Header.Get("X-SubAccount-ID")
	if !globalRulesEngine.EvaluateMFA(subAccountID) {
		http.Error(w, "MFA Required: You must enable MFA to verify domains.", http.StatusForbidden)
		return
	}

	var domainName, expectedPubKey string
	err := db.QueryRow("SELECT domain_name, COALESCE(dkim_public_key, '') FROM domains WHERE id = $1", domainID).Scan(&domainName, &expectedPubKey)
	if err != nil {
		http.Error(w, "Domain not found", http.StatusNotFound)
		return
	}

	// 1. Verify SPF.
	//
	// Phase 55: this used to accept ANY record starting with "v=spf1" as
	// proof the domain was set up right -- a domain with SPF configured
	// for Google Workspace and nothing else would pass, even though it
	// does not authorize us to send as that domain at all. domains_api.go
	// tells operators to publish "v=spf1 mx ~all", so actually check for
	// the "mx" mechanism (or an explicit include of our own hostname) --
	// not just that *a* SPF record of *some* kind exists.
	spfVerified := false
	txtRecords, _ := net.LookupTXT(domainName)
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

	// 2. Verify DKIM (Phase 38: Check CNAME Resolution)
	dkimVerified := false
	var expectedCnameTarget string
	err = db.QueryRow("SELECT COALESCE(cname_target, '') FROM domains WHERE id = $1", domainID).Scan(&expectedCnameTarget)
	
	if expectedCnameTarget != "" {
		dkimHost := "esp._domainkey." + domainName
		cname, _ := net.LookupCNAME(dkimHost)
		if cname != "" && strings.Contains(cname, expectedCnameTarget) {
			dkimVerified = true
		}
	}

	// 3. Verify DMARC (Phase 38: Strict Enforcement)
	dmarcVerified := false
	dmarcRecords, _ := net.LookupTXT("_dmarc." + domainName)
	for _, txt := range dmarcRecords {
		if strings.HasPrefix(txt, "v=DMARC1") {
			dmarcVerified = true
			break
		}
	}

	// Update Verification Status in DB
	isVerified := spfVerified && dkimVerified && dmarcVerified
	newStatus := "PENDING"
	if isVerified {
		newStatus = "VERIFIED"
	}
	
	_, err = db.Exec(`
		UPDATE domains 
		SET is_verified = $1, status = $2, verified_at = NOW()
		WHERE id = $3
	`, isVerified, newStatus, domainID)
	
	if err != nil {
		log.Printf("Failed to update domain verification status: %v", err)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"domain":         domainName,
		"spf_verified":   spfVerified,
		"dkim_verified":  dkimVerified,
		"dmarc_verified": dmarcVerified,
		"is_verified":    isVerified,
		"status":         newStatus,
	})
}
