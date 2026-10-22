package cryptomesh

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// Helper to generate deterministically formatted test hex keys (64 hex characters = 32 bytes)
func testHexKey(b byte) string {
	bytes := make([]byte, 32)
	for i := range bytes {
		bytes[i] = b
	}
	return hex.EncodeToString(bytes)
}

func testAuthKey(b byte) string {
	bytes := make([]byte, 64)
	for i := range bytes {
		bytes[i] = b
	}
	return hex.EncodeToString(bytes)
}

func TestRoundTripEncryption(t *testing.T) {
	cipher := NewTransitCipher()
	secret := testHexKey(0x11)
	authSecret := testAuthKey(0x22)

	type EmailPayload struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
		OrderID string `json:"orderId"`
	}

	original := EmailPayload{
		To:      "customer@example.pk",
		Subject: "Order Confirmed - ORD-PK-9842",
		Body:    "Your payment was received. Dispatch in progress.",
		OrderID: "ORD-PK-9842",
	}

	params := EncryptParams{
		TenantID:      "tenant-global-01",
		SenderID:      "newcom-esp",
		RecipientID:   "core-newax",
		KeyID:         "key-esp-01",
		AuthSecretHex: authSecret,
	}

	envelope, err := cipher.Encrypt(original, secret, params)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	if envelope.Ciphertext == "" || envelope.IV == "" || envelope.AuthTag == "" || envelope.Signature == "" {
		t.Fatalf("envelope missing required cryptographic fields: %+v", envelope)
	}

	opts := DecryptOptions{
		AuthSecretHex:      authSecret,
		EnforceReplayCheck: true,
	}

	decryptedBytes, err := cipher.Decrypt(envelope, secret, opts)
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}

	if !strings.Contains(string(decryptedBytes), "ORD-PK-9842") {
		t.Fatalf("decrypted payload does not match original: %s", string(decryptedBytes))
	}
}

func TestDualKeySeparationAndRelay(t *testing.T) {
	tenantID := "tenant-retail-corp"

	// Sender: newcom-esp (has Ingress Key 0xAA)
	senderCreds := ClientCredentials{
		EntityID:         "newcom-esp",
		TenantID:         tenantID,
		KeyID:            "key-esp-v1",
		IngressSecretHex: testHexKey(0xAA),
		EgressSecretHex:  testHexKey(0xBB),
		AuthSecretHex:    testAuthKey(0xCC),
	}

	// Receiver: erp-1 (has Egress Key 0xDD)
	receiverCreds := ClientCredentials{
		EntityID:         "erp-1",
		TenantID:         tenantID,
		KeyID:            "key-erp-v1",
		IngressSecretHex: testHexKey(0xEE),
		EgressSecretHex:  testHexKey(0xDD),
		AuthSecretHex:    testAuthKey(0xFF),
	}

	// Guarantee keys are completely separate
	if senderCreds.IngressSecretHex == receiverCreds.EgressSecretHex {
		t.Fatal("sender ingress key matches receiver egress key — key separation violated")
	}

	senderClient, err := NewCryptoMeshClient(senderCreds)
	if err != nil {
		t.Fatalf("failed to create sender client: %v", err)
	}

	receiverClient, err := NewCryptoMeshClient(receiverCreds)
	if err != nil {
		t.Fatalf("failed to create receiver client: %v", err)
	}

	payload := map[string]interface{}{
		"event":    "email.dispatch.completed",
		"messageId": "msg_88492048",
		"status":    "DELIVERED",
	}

	// 1. Sender encrypts for erp-1
	senderEnvelope, err := senderClient.EncryptForRelay("erp-1", payload)
	if err != nil {
		t.Fatalf("sender encryption failed: %v", err)
	}

	// 2. Direct decryption by receiver MUST FAIL (receiver does not have sender's ingress key)
	_, err = receiverClient.DecryptFromRelay(senderEnvelope)
	if err == nil {
		t.Fatal("receiver decrypted sender's envelope directly — expected cryptographic failure!")
	}

	// 3. Simulate NeuraCore Relay re-encryption:
	// Relay holds both sender Ingress Key (0xAA) and receiver Egress Key (0xDD)
	relayCipher := NewTransitCipher()

	// NeuraCore decrypts with sender's Ingress Key
	relayDecryptedBytes, err := relayCipher.Decrypt(senderEnvelope, senderCreds.IngressSecretHex, DecryptOptions{
		AuthSecretHex:      senderCreds.AuthSecretHex,
		EnforceReplayCheck: true,
	})
	if err != nil {
		t.Fatalf("NeuraCore failed to decrypt sender envelope: %v", err)
	}

	// NeuraCore re-encrypts with receiver's Egress Key
	reEncryptedEnvelope, err := relayCipher.Encrypt(relayDecryptedBytes, receiverCreds.EgressSecretHex, EncryptParams{
		TenantID:      tenantID,
		SenderID:      "newcom-esp",
		RecipientID:   "erp-1",
		KeyID:         receiverCreds.KeyID,
		AuthSecretHex: receiverCreds.AuthSecretHex,
	})
	if err != nil {
		t.Fatalf("NeuraCore failed to re-encrypt envelope: %v", err)
	}

	// Verify new ciphertext differs from original
	if reEncryptedEnvelope.Ciphertext == senderEnvelope.Ciphertext {
		t.Fatal("re-encrypted ciphertext matches original ciphertext — zero-knowledge isolation violated")
	}

	// 4. Receiver successfully decrypts re-encrypted envelope
	var receivedPayload map[string]interface{}
	err = receiverClient.DecryptFromRelayJSON(reEncryptedEnvelope, &receivedPayload)
	if err != nil {
		t.Fatalf("receiver failed to decrypt relayed envelope: %v", err)
	}

	if receivedPayload["messageId"] != "msg_88492048" {
		t.Fatalf("relayed payload corrupted: %+v", receivedPayload)
	}
}

func TestHMACIntegrityTamperDetection(t *testing.T) {
	cipher := NewTransitCipher()
	secret := testHexKey(0x33)
	authSecret := testAuthKey(0x44)

	params := EncryptParams{
		TenantID:      "tenant-01",
		SenderID:      "node-a",
		RecipientID:   "node-b",
		KeyID:         "key-1",
		AuthSecretHex: authSecret,
	}

	envelope, err := cipher.Encrypt("Sensitive financial message", secret, params)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	opts := DecryptOptions{
		AuthSecretHex:      authSecret,
		EnforceReplayCheck: true,
	}

	// Tamper with ciphertext
	tampered := *envelope
	tampered.Ciphertext = tampered.Ciphertext[:len(tampered.Ciphertext)-4] + "AAAA"

	_, err = cipher.Decrypt(&tampered, secret, opts)
	if err != ErrCryptographicIntegrity {
		t.Fatalf("expected ErrCryptographicIntegrity on tampered ciphertext, got: %v", err)
	}

	// Tamper with IV
	tamperedIV := *envelope
	tamperedIV.IV = "BBBBBBBBBBBB"

	_, err = cipher.Decrypt(&tamperedIV, secret, opts)
	if err != ErrCryptographicIntegrity {
		t.Fatalf("expected ErrCryptographicIntegrity on tampered IV, got: %v", err)
	}
}

func TestReplayAttackDetection(t *testing.T) {
	cipher := NewTransitCipher()
	secret := testHexKey(0x55)
	authSecret := testAuthKey(0x66)

	// Custom timestamp from 15 minutes ago
	oldTime := time.Now().UTC().Add(-15 * time.Minute).Format(time.RFC3339)

	params := EncryptParams{
		TenantID:        "tenant-01",
		SenderID:        "node-a",
		RecipientID:     "node-b",
		KeyID:           "key-1",
		AuthSecretHex:   authSecret,
		CustomTimestamp: oldTime,
	}

	envelope, err := cipher.Encrypt("Replay attack payload", secret, params)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	opts := DecryptOptions{
		AuthSecretHex:       authSecret,
		MaxClockSkewSeconds: 300, // 5 min threshold
		EnforceReplayCheck:  true,
	}

	_, err = cipher.Decrypt(envelope, secret, opts)
	if err != ErrReplayAttack {
		t.Fatalf("expected ErrReplayAttack for old timestamp, got: %v", err)
	}
}

func TestTenantAndRecipientValidation(t *testing.T) {
	creds := ClientCredentials{
		EntityID:         "target-worker",
		TenantID:         "tenant-alpha",
		KeyID:            "key-1",
		IngressSecretHex: testHexKey(0x77),
		EgressSecretHex:  testHexKey(0x88),
		AuthSecretHex:    testAuthKey(0x99),
	}

	client, err := NewCryptoMeshClient(creds)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// Foreign tenant envelope
	foreignTenantEnvelope := &EncryptedTransitEnvelope{
		Version:     ProtocolVersion,
		TenantID:    "tenant-bravo",
		SenderID:    "other-sender",
		RecipientID: "target-worker",
	}

	_, err = client.DecryptFromRelay(foreignTenantEnvelope)
	if err == nil || !strings.Contains(err.Error(), "cross-tenant access violation") {
		t.Fatalf("expected cross-tenant violation, got: %v", err)
	}

	// Mismatched recipient envelope
	mismatchedRecipientEnvelope := &EncryptedTransitEnvelope{
		Version:     ProtocolVersion,
		TenantID:    "tenant-alpha",
		SenderID:    "sender-1",
		RecipientID: "wrong-recipient",
	}

	_, err = client.DecryptFromRelay(mismatchedRecipientEnvelope)
	if err == nil || !strings.Contains(err.Error(), "mismatched recipient") {
		t.Fatalf("expected recipient mismatch error, got: %v", err)
	}
}
