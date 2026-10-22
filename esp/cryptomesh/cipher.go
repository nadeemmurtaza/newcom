package cryptomesh

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	ProtocolVersion      = "1.0.0"
	DefaultIVLength      = 12
	DefaultAuthTagLength = 16
	DefaultMaxSkewSec    = 300 // 5 minutes
)

// TransitCipher implements the core AES-256-GCM authenticated cipher and HMAC-SHA512 integrity signing.
type TransitCipher struct{}

// NewTransitCipher returns a new TransitCipher instance.
func NewTransitCipher() *TransitCipher {
	return &TransitCipher{}
}

// BuildAAD constructs the Additional Authenticated Data binding routing headers to the cipher.
func (c *TransitCipher) BuildAAD(version, tenantID, senderID, recipientID, keyID, timestamp string) []byte {
	return []byte(fmt.Sprintf("%s:%s:%s:%s:%s:%s", version, tenantID, senderID, recipientID, keyID, timestamp))
}

// BuildCanonicalString constructs the delimited string over which the HMAC-SHA512 signature is computed.
func (c *TransitCipher) BuildCanonicalString(env *EncryptedTransitEnvelope) string {
	parts := []string{
		env.Version,
		env.TenantID,
		env.SenderID,
		env.RecipientID,
		env.KeyID,
		env.Timestamp,
		env.IV,
		env.AuthTag,
		env.Ciphertext,
	}
	return strings.Join(parts, "|")
}

// ComputeSignature calculates the 512-bit HMAC-SHA-512 signature of the canonical envelope.
func (c *TransitCipher) ComputeSignature(env *EncryptedTransitEnvelope, authSecretHex string) (string, error) {
	key, err := hex.DecodeString(authSecretHex)
	if err != nil {
		return "", fmt.Errorf("invalid authSecretHex: %w", err)
	}

	canonical := c.BuildCanonicalString(env)
	mac := hmac.New(sha512.New, key)
	mac.Write([]byte(canonical))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Encrypt encrypts a serializable payload using AES-256-GCM and computes a 512-bit HMAC-SHA-512 signature.
func (c *TransitCipher) Encrypt(payload interface{}, secretHex string, params EncryptParams) (*EncryptedTransitEnvelope, error) {
	keyBytes, err := hex.DecodeString(secretHex)
	if err != nil || len(keyBytes) != 32 {
		return nil, ErrKeyInvalid
	}

	var serialized []byte
	switch p := payload.(type) {
	case []byte:
		serialized = p
	case string:
		serialized = []byte(p)
	default:
		serialized, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize payload to JSON: %w", err)
		}
	}

	iv := make([]byte, DefaultIVLength)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("failed to generate random IV: %w", err)
	}

	timestamp := params.CustomTimestamp
	if timestamp == "" {
		timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher block: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM AEAD cipher: %w", err)
	}

	aad := c.BuildAAD(ProtocolVersion, params.TenantID, params.SenderID, params.RecipientID, params.KeyID, timestamp)
	sealed := gcm.Seal(nil, iv, serialized, aad)

	tagLen := gcm.Overhead()
	if len(sealed) < tagLen {
		return nil, errors.New("sealed ciphertext shorter than AEAD tag overhead")
	}

	ciphertext := sealed[:len(sealed)-tagLen]
	authTag := sealed[len(sealed)-tagLen:]

	env := &EncryptedTransitEnvelope{
		Version:     ProtocolVersion,
		TenantID:    params.TenantID,
		SenderID:    params.SenderID,
		RecipientID: params.RecipientID,
		IV:          base64.StdEncoding.EncodeToString(iv),
		Ciphertext:  base64.StdEncoding.EncodeToString(ciphertext),
		AuthTag:     base64.StdEncoding.EncodeToString(authTag),
		KeyID:       params.KeyID,
		Timestamp:   timestamp,
	}

	sig, err := c.ComputeSignature(env, params.AuthSecretHex)
	if err != nil {
		return nil, err
	}
	env.Signature = sig

	return env, nil
}

// Decrypt decrypts an EncryptedTransitEnvelope, verifying anti-replay, HMAC-SHA512 integrity, and AEAD tags.
func (c *TransitCipher) Decrypt(env *EncryptedTransitEnvelope, secretHex string, opts DecryptOptions) ([]byte, error) {
	keyBytes, err := hex.DecodeString(secretHex)
	if err != nil || len(keyBytes) != 32 {
		return nil, ErrKeyInvalid
	}

	// 1. Replay attack check
	if opts.EnforceReplayCheck {
		t, err := time.Parse(time.RFC3339Nano, env.Timestamp)
		if err != nil {
			t, err = time.Parse(time.RFC3339, env.Timestamp)
			if err != nil {
				return nil, errors.New("malformed envelope timestamp format")
			}
		}

		maxSkew := opts.MaxClockSkewSeconds
		if maxSkew <= 0 {
			maxSkew = DefaultMaxSkewSec
		}

		delta := time.Since(t)
		if delta < 0 {
			delta = -delta
		}
		if delta > time.Duration(maxSkew)*time.Second {
			return nil, ErrReplayAttack
		}
	}

	// 2. HMAC-SHA512 signature check
	if opts.AuthSecretHex != "" {
		expectedSig, err := c.ComputeSignature(env, opts.AuthSecretHex)
		if err != nil {
			return nil, fmt.Errorf("failed to compute verification signature: %w", err)
		}

		actualSigBytes, err := base64.StdEncoding.DecodeString(env.Signature)
		if err != nil {
			return nil, ErrCryptographicIntegrity
		}

		expectedSigBytes, err := base64.StdEncoding.DecodeString(expectedSig)
		if err != nil {
			return nil, ErrCryptographicIntegrity
		}

		if subtle.ConstantTimeCompare(actualSigBytes, expectedSigBytes) != 1 {
			return nil, ErrCryptographicIntegrity
		}
	}

	// 3. AES-GCM Decryption
	ivBytes, err := base64.StdEncoding.DecodeString(env.IV)
	if err != nil || len(ivBytes) != DefaultIVLength {
		return nil, errors.New("invalid IV in envelope")
	}

	ciphertextBytes, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return nil, errors.New("invalid ciphertext base64")
	}

	authTagBytes, err := base64.StdEncoding.DecodeString(env.AuthTag)
	if err != nil || len(authTagBytes) != DefaultAuthTagLength {
		return nil, errors.New("invalid authTag in envelope")
	}

	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher block: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM AEAD cipher: %w", err)
	}

	aad := c.BuildAAD(env.Version, env.TenantID, env.SenderID, env.RecipientID, env.KeyID, env.Timestamp)
	sealedWithTag := append(ciphertextBytes, authTagBytes...)

	plaintext, err := gcm.Open(nil, ivBytes, sealedWithTag, aad)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}
