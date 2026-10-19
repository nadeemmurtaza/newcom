package main

import (
	"bytes"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/emersion/go-msgauth/dkim"
)

// Phase 53: Outbound DKIM Signing
//
// Domains already had DKIM keys generated at creation (domains_api.go) and
// published as DNS records, but nothing ever signed outgoing mail with
// them -- dkimPrivateKey just sat in the database unused. This builds the
// raw RFC 5322 message and signs it before it's handed to PerformHandshake.

const dkimSelector = "esp" // must match the "esp._domainkey.<domain>" host used in domains_api.go / dns_checker.go

// buildRawMessage assembles a minimal RFC 5322 message from the fields the
// send API already collects. This is also the payload DKIM signs, so the
// headers here must be the same ones actually transmitted.
func buildRawMessage(from, to, subject, body, messageID string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	if messageID != "" {
		fmt.Fprintf(&buf, "Message-Id: <%s@%s>\r\n", messageID, domainPartOf(addressOnly(from)))
	}
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(body)
	return buf.Bytes()
}

// signOutboundMessage signs raw with the From address's domain DKIM key, if
// that domain exists and has one. On any failure it logs and returns the
// unsigned message rather than blocking delivery -- an unsigned send that
// lands in spam is recoverable; silently dropping mail because signing
// failed is worse.
func signOutboundMessage(db *sql.DB, raw []byte, fromAddress string) []byte {
	domain := domainPartOf(addressOnly(fromAddress))
	if domain == "" {
		return raw
	}

	var privPEM string
	err := db.QueryRow("SELECT COALESCE(dkim_private_key, '') FROM domains WHERE domain_name = $1", domain).Scan(&privPEM)
	if err != nil || privPEM == "" {
		log.Printf("DKIM: no key for domain %s, sending unsigned", domain)
		return raw
	}

	block, _ := pem.Decode([]byte(privPEM))
	if block == nil {
		log.Printf("DKIM: failed to decode PEM for domain %s, sending unsigned", domain)
		return raw
	}
	privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		log.Printf("DKIM: failed to parse private key for domain %s: %v; sending unsigned", domain, err)
		return raw
	}

	var signed bytes.Buffer
	err = dkim.Sign(&signed, bytes.NewReader(raw), &dkim.SignOptions{
		Domain:   domain,
		Selector: dkimSelector,
		Signer:   privKey,
	})
	if err != nil {
		log.Printf("DKIM: signing failed for domain %s: %v; sending unsigned", domain, err)
		return raw
	}
	return signed.Bytes()
}

func addressOnly(from string) string {
	// From headers are sometimes "Name <addr@domain>"; domainPartOf needs
	// the bare address.
	if i := strings.LastIndex(from, "<"); i >= 0 {
		if j := strings.Index(from[i:], ">"); j > 0 {
			return from[i+1 : i+j]
		}
	}
	return from
}
