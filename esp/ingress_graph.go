package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// ==========================================
// MODULE 1: Gmail Ingress & Graph Threading Engine
// ==========================================

// CryptoValidationState represents the cryptographic verification state of an inbound email
type CryptoValidationState struct {
	SPFStatus   string // Pass, Fail, SoftFail, Neutral, None
	DKIMStatus  string // Pass, Fail, None
	DMARCStatus string // Pass, Fail, None
}

// InboundEmailPacket represents the structural model of a received email
type InboundEmailPacket struct {
	PacketID    string
	From        string
	To          []string
	Subject     string
	Body        string
	Headers     map[string]string
	MessageID   string
	InReplyTo   string
	References  []string
	Validation  CryptoValidationState
	ReceivedAt  time.Time
}

// ThreadNode represents a single email in the graph
type ThreadNode struct {
	MessageID string
	ParentID  string // InReplyTo
	ThreadID  string
	CreatedAt time.Time
}

// ThreadGraphStore defines the transactional data store interface for the graph
type ThreadGraphStore interface {
	GetThreadIDByMessageID(ctx context.Context, messageID string) (string, error)
	GetThreadIDByReferences(ctx context.Context, references []string) (string, error)
	CreateThread(ctx context.Context) (string, error)
	LinkMessageToThread(ctx context.Context, messageID, threadID, parentID string) error
}

// InMemoryGraphStore is a concurrent, robust in-memory implementation of the ThreadGraphStore
type InMemoryGraphStore struct {
	mu           sync.RWMutex
	messageIndex map[string]ThreadNode
	threadMap    map[string][]string // ThreadID -> []MessageID
}

func NewInMemoryGraphStore() *InMemoryGraphStore {
	store := &InMemoryGraphStore{
		messageIndex: make(map[string]ThreadNode),
		threadMap:    make(map[string][]string),
	}

	// Phase 50: Background Eviction (OOM Prevention)
	go func() {
		for {
			time.Sleep(1 * time.Hour)
			store.mu.Lock()
			threshold := time.Now().UTC().Add(-30 * 24 * time.Hour)
			for msgID, node := range store.messageIndex {
				if node.CreatedAt.Before(threshold) {
					delete(store.messageIndex, msgID)
				}
			}
			store.mu.Unlock()
		}
	}()

	return store
}

func (store *InMemoryGraphStore) GetThreadIDByMessageID(ctx context.Context, messageID string) (string, error) {
	if messageID == "" {
		return "", fmt.Errorf("invalid message ID")
	}

	store.mu.RLock()
	defer store.mu.RUnlock()

	if node, exists := store.messageIndex[messageID]; exists {
		return node.ThreadID, nil
	}
	return "", fmt.Errorf("message not found")
}

func (store *InMemoryGraphStore) GetThreadIDByReferences(ctx context.Context, references []string) (string, error) {
	if len(references) == 0 {
		return "", fmt.Errorf("no references provided")
	}

	store.mu.RLock()
	defer store.mu.RUnlock()

	// Traverse references in reverse order (most recent first) to find the nearest valid ThreadID
	for i := len(references) - 1; i >= 0; i-- {
		ref := references[i]
		if node, exists := store.messageIndex[ref]; exists {
			return node.ThreadID, nil
		}
	}

	return "", fmt.Errorf("no thread found for given references")
}

func (store *InMemoryGraphStore) CreateThread(ctx context.Context) (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate secure Thread-ID: %w", err)
	}
	threadID := "thread_" + hex.EncodeToString(bytes)
	
	store.mu.Lock()
	defer store.mu.Unlock()
	
	// Pre-allocate the slice for the new thread
	store.threadMap[threadID] = make([]string, 0, 10)
	
	return threadID, nil
}

func (store *InMemoryGraphStore) LinkMessageToThread(ctx context.Context, messageID, threadID, parentID string) error {
	if messageID == "" || threadID == "" {
		return fmt.Errorf("messageID and threadID cannot be empty")
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if _, exists := store.messageIndex[messageID]; exists {
		return fmt.Errorf("message %s already linked", messageID)
	}

	node := ThreadNode{
		MessageID: messageID,
		ParentID:  parentID,
		ThreadID:  threadID,
		CreatedAt: time.Now().UTC(),
	}

	store.messageIndex[messageID] = node
	store.threadMap[threadID] = append(store.threadMap[threadID], messageID)

	return nil
}

// GraphThreadingEngine processes inbound packets and maps them to conversation threads
type GraphThreadingEngine struct {
	store ThreadGraphStore
}

func NewGraphThreadingEngine(store ThreadGraphStore) *GraphThreadingEngine {
	return &GraphThreadingEngine{
		store: store,
	}
}

// parseReferences safely parses space/newline-separated Reference strings into arrays
func parseReferences(refStr string) []string {
	if refStr == "" {
		return []string{}
	}
	
	// Fields handles tabs, newlines, and multiple spaces natively
	rawRefs := strings.Fields(refStr)
	cleanedRefs := make([]string, 0, len(rawRefs))
	
	for _, r := range rawRefs {
		r = strings.TrimSpace(r)
		// Clean < > wrappers if present
		r = strings.TrimPrefix(r, "<")
		r = strings.TrimSuffix(r, ">")
		if r != "" {
			cleanedRefs = append(cleanedRefs, r)
		}
	}
	return cleanedRefs
}

// ProcessPacket executes the core Graph Threading logic
func (engine *GraphThreadingEngine) ProcessPacket(ctx context.Context, packet *InboundEmailPacket) (string, error) {
	if packet == nil {
		return "", fmt.Errorf("cannot process nil packet")
	}

	// 1. Extract Core Identifiers safely
	messageID := packet.MessageID
	if messageID == "" {
		// Fallback: Check Headers if standard property is missing
		messageID = packet.Headers["Message-ID"]
	}
	
	inReplyTo := packet.InReplyTo
	if inReplyTo == "" {
		inReplyTo = packet.Headers["In-Reply-To"]
	}
	
	// Sanitize angle brackets
	messageID = strings.Trim(messageID, "<>")
	inReplyTo = strings.Trim(inReplyTo, "<>")

	if messageID == "" {
		return "", fmt.Errorf("fatal error: inbound packet missing Message-ID, cannot construct graph")
	}

	// 2. Extract and Parse References
	var refs []string
	if len(packet.References) > 0 {
		refs = packet.References
	} else if headerRefs, exists := packet.Headers["References"]; exists {
		refs = parseReferences(headerRefs)
	}
	
	// Add In-Reply-To to the end of References if it exists and isn't already there
	if inReplyTo != "" {
		found := false
		for _, r := range refs {
			if r == inReplyTo {
				found = true
				break
			}
		}
		if !found {
			refs = append(refs, inReplyTo)
		}
	}

	// 3. Thread Resolution Algorithm
	var targetThreadID string
	var err error

	// Attempt resolution via direct parent (In-Reply-To)
	if inReplyTo != "" {
		targetThreadID, err = engine.store.GetThreadIDByMessageID(ctx, inReplyTo)
		if err != nil {
			log.Printf("[ThreadEngine] In-Reply-To (%s) not found in graph. Will fallback to references.", inReplyTo)
		}
	}

	// Fallback resolution via References array
	if targetThreadID == "" && len(refs) > 0 {
		targetThreadID, err = engine.store.GetThreadIDByReferences(ctx, refs)
		if err != nil {
			log.Printf("[ThreadEngine] References chain resolution failed. A new thread will be minted.")
		}
	}

	// 4. Minting or Appending
	if targetThreadID == "" {
		// No linkages found. Mint a new globally unique Thread-ID using atomic operations.
		targetThreadID, err = engine.store.CreateThread(ctx)
		if err != nil {
			return "", fmt.Errorf("critical failure minting new thread: %w", err)
		}
		log.Printf("[ThreadEngine] Minted new Thread-ID [%s] for Message-ID [%s]", targetThreadID, messageID)
	} else {
		log.Printf("[ThreadEngine] Linking Message-ID [%s] to existing Thread-ID [%s] via Parent [%s]", messageID, targetThreadID, inReplyTo)
	}

	// 5. Commit linkage to graph
	if err := engine.store.LinkMessageToThread(ctx, messageID, targetThreadID, inReplyTo); err != nil {
		return "", fmt.Errorf("critical failure linking message to thread: %w", err)
	}

	return targetThreadID, nil
}
