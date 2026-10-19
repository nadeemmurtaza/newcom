package main

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jhillyerd/enmime"
	smtp "github.com/emersion/go-smtp"
	"github.com/redis/go-redis/v9"
)

// Phase 50: Real Inbound SMTP Receiver
//
// This replaces the never-implemented C++ smtp-engine stub (which only
// logged simulated strings) with a working RFC 5321 receiver. It accepts
// mail for any verified domain this org owns, resolves the recipient
// through MailboxManager (alias / catch-all aware), and persists the
// message so it actually shows up via the mailbox API.

const maxInboundMessageBytes = 25 * 1024 * 1024 // 25MB

type inboundBackend struct {
	db  *sql.DB
	rdb *redis.Client
}

func (b *inboundBackend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &inboundSession{db: b.db, rdb: b.rdb}, nil
}

type inboundSession struct {
	db  *sql.DB
	rdb *redis.Client

	from       string
	recipients []inboundRecipient
}

type inboundRecipient struct {
	address  string
	domainID string
	orgID    string
}

func (s *inboundSession) Mail(from string, opts *smtp.MailOptions) error {
	s.from = from
	s.recipients = nil
	return nil
}

func (s *inboundSession) Rcpt(to string, opts *smtp.RcptOptions) error {
	domain := domainPartOf(to)
	if domain == "" {
		return &smtp.SMTPError{Code: 501, Message: "5.1.3 Bad recipient address syntax"}
	}

	var domainID, orgID string
	err := s.db.QueryRow(`
		SELECT id, org_id FROM domains WHERE domain_name = $1
	`, domain).Scan(&domainID, &orgID)
	if err != nil {
		log.Printf("Inbound SMTP: rejecting RCPT for unknown domain %q", domain)
		return &smtp.SMTPError{Code: 550, Message: "5.1.1 No such domain here"}
	}

	s.recipients = append(s.recipients, inboundRecipient{address: strings.ToLower(to), domainID: domainID, orgID: orgID})
	return nil
}

func (s *inboundSession) Data(r io.Reader) error {
	limited := io.LimitReader(r, maxInboundMessageBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return &smtp.SMTPError{Code: 451, Message: "4.3.0 Failed to read message"}
	}
	if len(raw) > maxInboundMessageBytes {
		return &smtp.SMTPError{Code: 552, Message: "5.3.4 Message too large"}
	}

	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		log.Printf("Inbound SMTP: failed to parse MIME message from %s: %v", s.from, err)
		return &smtp.SMTPError{Code: 554, Message: "5.6.0 Unable to parse message"}
	}

	subject := env.GetHeader("Subject")
	threadID := env.GetHeader("Message-Id")
	if threadID == "" {
		threadID = uuid.NewString()
	}

	recipientList := make([]string, 0, len(s.recipients))
	for _, rcpt := range s.recipients {
		recipientList = append(recipientList, rcpt.address)
	}
	recipientsJoined := strings.Join(recipientList, ", ")

	delivered := 0
	for _, rcpt := range s.recipients {
		if err := persistInboundMessage(s.db, s.rdb, rcpt.orgID, rcpt.domainID, s.from, rcpt.address, recipientsJoined, subject, env.Text, env.HTML, threadID); err != nil {
			log.Printf("Inbound SMTP: failed to deliver to %s: %v", rcpt.address, err)
			continue
		}
		delivered++
	}

	if delivered == 0 {
		return &smtp.SMTPError{Code: 550, Message: "5.1.1 No such user here"}
	}
	return nil
}

func (s *inboundSession) Reset()        { s.from = ""; s.recipients = nil }
func (s *inboundSession) Logout() error { return nil }

func domainPartOf(address string) string {
	idx := strings.LastIndex(address, "@")
	if idx < 0 || idx == len(address)-1 {
		return ""
	}
	return strings.ToLower(address[idx+1:])
}

// persistInboundMessage resolves the recipient (alias/catch-all aware),
// classifies it into a folder, and writes the row. Shared by both the raw
// SMTP receiver above and the HTTP /v1/inbound intake path.
func persistInboundMessage(db *sql.DB, rdb *redis.Client, orgID, domainID, from, to, recipientsJoined, subject, bodyText, bodyHTML, threadID string) error {
	mailboxID, err := mailboxManager.ResolveRecipient(domainID, to)
	if err != nil {
		return err
	}

	bodyForClassification := bodyText
	if bodyForClassification == "" {
		bodyForClassification = bodyHTML
	}
	folderID := mailboxManager.ClassifyInbound(mailboxID, subject, bodyForClassification)
	if folderID == "" {
		// Fall back to INBOX if classification couldn't resolve a folder
		// (e.g. folders weren't provisioned yet for an old mailbox).
		_ = db.QueryRow("SELECT id FROM folders WHERE mailbox_id = $1 AND name = 'INBOX'", mailboxID).Scan(&folderID)
	}
	if folderID == "" {
		return sql.ErrNoRows
	}

	snippet := bodyForClassification
	if len(snippet) > 240 {
		snippet = snippet[:240]
	}

	// messages has a tenant_isolation_policy RLS policy keyed off the
	// app.current_org_id session setting. Nothing in this codebase ever set
	// it, so every insert into messages was silently impossible -- set it
	// with set_config's is_local=true so it only applies within this
	// transaction and never leaks across pooled connections.
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("SELECT set_config('app.current_org_id', $1, true)", orgID); err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO messages (org_id, mailbox_id, folder_id, sender, recipients, subject, snippet, body_text, body_html, thread_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, orgID, mailboxID, folderID, from, recipientsJoined, subject, snippet, bodyText, bodyHTML, threadID)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if rdb != nil {
		QueueWebhookEvent(rdb, WebhookEvent{
			OrgID:     orgID,
			MessageID: mailboxID,
			EventType: "inbound_received",
			Metadata:  to,
			Timestamp: time.Now().Unix(),
		})
	}
	return nil
}

func inboundSMTPPort() string {
	if p := os.Getenv("SMTP_PORT"); p != "" {
		if _, err := strconv.Atoi(p); err == nil {
			return p
		}
	}
	return "2525"
}

// startInboundSMTPServer runs the SMTP listener until ctx is cancelled.
func startInboundSMTPServer(ctx context.Context, wg *sync.WaitGroup, db *sql.DB, rdb *redis.Client) {
	defer wg.Done()

	be := &inboundBackend{db: db, rdb: rdb}
	srv := smtp.NewServer(be)
	srv.Addr = net.JoinHostPort("0.0.0.0", inboundSMTPPort())
	srv.Domain = mailHostname()
	srv.MaxMessageBytes = maxInboundMessageBytes
	srv.MaxRecipients = 100
	srv.AllowInsecureAuth = true
	srv.ReadTimeout = 60 * time.Second
	srv.WriteTimeout = 60 * time.Second

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("Inbound SMTP: shutdown error: %v", err)
		}
	}()

	log.Printf("Inbound SMTP Receiver listening on %s (domain=%s)", srv.Addr, srv.Domain)
	if err := srv.ListenAndServe(); err != nil && err != smtp.ErrServerClosed {
		log.Printf("Inbound SMTP: server error: %v", err)
	}
}
