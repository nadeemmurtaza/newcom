package cryptomesh

import (
	"errors"
)

var (
	// ErrCryptographicIntegrity is returned when payload or signature integrity checks fail.
	ErrCryptographicIntegrity = errors.New("cryptographic integrity check failed")

	// ErrReplayAttack is returned when message timestamp falls outside the acceptable clock skew window.
	ErrReplayAttack = errors.New("replay attack detected: message timestamp outside acceptable window")

	// ErrKeyInvalid is returned when a secret key does not meet AES-256 (32 bytes) requirements.
	ErrKeyInvalid = errors.New("invalid key length: expected 32 bytes (64 hex characters) for AES-256")

	// ErrDecryptionFailed is returned when AES-GCM authentication fails or payload is corrupt.
	ErrDecryptionFailed = errors.New("decryption failed: AEAD authentication tag mismatch or corrupt ciphertext")

	// ErrCrossTenantViolation is returned when an envelope does not match the client's tenant boundary.
	ErrCrossTenantViolation = errors.New("cross-tenant access violation: tenant mismatch")

	// ErrRecipientMismatch is returned when an envelope was addressed to a different entity.
	ErrRecipientMismatch = errors.New("mismatched recipient: envelope addressed to another entity")
)

// EncryptedTransitEnvelope matches the NEWAX banking-grade multi-layer crypto mesh specification.
type EncryptedTransitEnvelope struct {
	Version             string `json:"version"`
	TenantID            string `json:"tenantId"`
	SenderID            string `json:"senderId"`
	RecipientID         string `json:"recipientId"`
	IV                  string `json:"iv"`
	Ciphertext          string `json:"ciphertext"`
	AuthTag             string `json:"authTag"`
	KeyID               string `json:"keyId"`
	Timestamp           string `json:"timestamp"`
	Signature           string `json:"signature"`
	AsymmetricSignature string `json:"asymmetricSignature,omitempty"`
}

// ClientCredentials defines the sovereign keys held by an entity.
// Senders and Receivers have strictly different keys.
type ClientCredentials struct {
	EntityID         string `json:"entityId"`
	TenantID         string `json:"tenantId"`
	KeyID            string `json:"keyId"`
	IngressSecretHex string `json:"ingressSecretHex"` // 256-bit AES-GCM key to encrypt outbound to NeuraCore
	EgressSecretHex  string `json:"egressSecretHex"`  // 256-bit AES-GCM key to decrypt inbound from NeuraCore
	AuthSecretHex    string `json:"authSecretHex"`    // 512-bit HMAC-SHA512 key for message signatures
}

// EncryptParams configures outbound envelope creation.
type EncryptParams struct {
	TenantID        string
	SenderID        string
	RecipientID     string
	KeyID           string
	AuthSecretHex   string
	CustomTimestamp string
}

// DecryptOptions configures inbound envelope verification.
type DecryptOptions struct {
	AuthSecretHex       string
	MaxClockSkewSeconds int
	EnforceReplayCheck  bool
}
