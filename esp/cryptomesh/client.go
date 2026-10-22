package cryptomesh

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CryptoMeshClient is the lightweight Zero-Knowledge client adapter for Go services.
// It holds ONLY the entity's sovereign keys:
// - IngressSecretHex: 256-bit AES key to encrypt outbound messages for NeuraCore
// - EgressSecretHex:  256-bit AES key to decrypt inbound messages from NeuraCore
// - AuthSecretHex:    512-bit HMAC-SHA-512 key for message integrity
type CryptoMeshClient struct {
	creds  ClientCredentials
	cipher *TransitCipher
	client *http.Client
}

// NewCryptoMeshClient creates and initializes a new CryptoMeshClient.
func NewCryptoMeshClient(creds ClientCredentials) (*CryptoMeshClient, error) {
	if creds.EntityID == "" {
		return nil, errors.New("entityId is required")
	}
	if creds.TenantID == "" {
		return nil, errors.New("tenantId is required")
	}
	if len(creds.IngressSecretHex) != 64 {
		return nil, fmt.Errorf("ingressSecretHex must be 64 hex characters (32 bytes), got %d", len(creds.IngressSecretHex))
	}
	if len(creds.EgressSecretHex) != 64 {
		return nil, fmt.Errorf("egressSecretHex must be 64 hex characters (32 bytes), got %d", len(creds.EgressSecretHex))
	}
	if len(creds.AuthSecretHex) < 64 {
		return nil, fmt.Errorf("authSecretHex must be at least 64 hex characters, got %d", len(creds.AuthSecretHex))
	}

	keyID := creds.KeyID
	if keyID == "" {
		keyID = fmt.Sprintf("key_%s_v1", creds.EntityID)
	}
	creds.KeyID = keyID

	return &CryptoMeshClient{
		creds:  creds,
		cipher: NewTransitCipher(),
		client: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// EntityID returns the client's registered entity identifier.
func (c *CryptoMeshClient) EntityID() string {
	return c.creds.EntityID
}

// TenantID returns the multi-tenant partition identifier.
func (c *CryptoMeshClient) TenantID() string {
	return c.creds.TenantID
}

// EncryptForRelay encrypts an outbound message for NeuraCore to relay to recipientID.
func (c *CryptoMeshClient) EncryptForRelay(recipientID string, payload interface{}) (*EncryptedTransitEnvelope, error) {
	params := EncryptParams{
		TenantID:      c.creds.TenantID,
		SenderID:      c.creds.EntityID,
		RecipientID:   recipientID,
		KeyID:         c.creds.KeyID,
		AuthSecretHex: c.creds.AuthSecretHex,
	}

	return c.cipher.Encrypt(payload, c.creds.IngressSecretHex, params)
}

// DecryptFromRelay validates and decrypts an inbound envelope forwarded by NeuraCore.
func (c *CryptoMeshClient) DecryptFromRelay(env *EncryptedTransitEnvelope) ([]byte, error) {
	if env == nil {
		return nil, errors.New("envelope cannot be nil")
	}

	// 1. Cross-tenant isolation verification
	if env.TenantID != c.creds.TenantID {
		return nil, fmt.Errorf("%w: message belongs to '%s', client bound to '%s'",
			ErrCrossTenantViolation, env.TenantID, c.creds.TenantID)
	}

	// 2. Intended recipient verification
	if env.RecipientID != c.creds.EntityID {
		return nil, fmt.Errorf("%w: envelope addressed to '%s', but this entity is '%s'",
			ErrRecipientMismatch, env.RecipientID, c.creds.EntityID)
	}

	opts := DecryptOptions{
		AuthSecretHex:      c.creds.AuthSecretHex,
		EnforceReplayCheck: true,
	}

	return c.cipher.Decrypt(env, c.creds.EgressSecretHex, opts)
}

// DecryptFromRelayJSON decrypts an inbound envelope and unmarshals the JSON into target.
func (c *CryptoMeshClient) DecryptFromRelayJSON(env *EncryptedTransitEnvelope, target interface{}) error {
	plaintext, err := c.DecryptFromRelay(env)
	if err != nil {
		return err
	}
	return json.Unmarshal(plaintext, target)
}

// RelayResponse is returned when transmitting an envelope through the central NeuraCore Relay.
type RelayResponse struct {
	Success        bool                      `json:"success"`
	EgressEnvelope *EncryptedTransitEnvelope `json:"egressEnvelope"`
	TotalRelayMs   float64                   `json:"totalRelayMs"`
	Error          string                    `json:"error,omitempty"`
}

// TransmitThroughRelay sends an outbound envelope to NeuraCore Relay and returns the re-encrypted egress envelope.
func (c *CryptoMeshClient) TransmitThroughRelay(relayURL string, env *EncryptedTransitEnvelope) (*EncryptedTransitEnvelope, error) {
	bodyBytes, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal envelope: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v1/crypto-mesh/relay", strings.TrimRight(relayURL, "/"))
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create relay HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("neuraCore relay connection failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read relay response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("relay returned HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var relayResp RelayResponse
	if err := json.Unmarshal(respBytes, &relayResp); err != nil {
		return nil, fmt.Errorf("failed to parse relay JSON response: %w", err)
	}

	if !relayResp.Success || relayResp.EgressEnvelope == nil {
		return nil, fmt.Errorf("relay processing failed: %s", relayResp.Error)
	}

	return relayResp.EgressEnvelope, nil
}
