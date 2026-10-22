package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/esp-project/core/cryptomesh"
)

var globalCryptoMeshClient *cryptomesh.CryptoMeshClient

// initCryptoMesh initializes the banking-grade Zero-Trust Crypto Mesh client for newcom/esp.
func initCryptoMesh() {
	enabled := os.Getenv("CRYPTO_MESH_ENABLED") == "true"
	ingressHex := os.Getenv("CRYPTO_MESH_INGRESS_SECRET_HEX")
	egressHex := os.Getenv("CRYPTO_MESH_EGRESS_SECRET_HEX")
	authHex := os.Getenv("CRYPTO_MESH_AUTH_SECRET_HEX")

	if !enabled && (ingressHex == "" || egressHex == "" || authHex == "") {
		log.Println("[CryptoMesh] Zero-Trust transit encryption is disabled or unconfigured in newcom/esp.")
		return
	}

	tenantID := os.Getenv("CRYPTO_MESH_TENANT_ID")
	if tenantID == "" {
		tenantID = "tenant_default"
	}

	entityID := os.Getenv("CRYPTO_MESH_ENTITY_ID")
	if entityID == "" {
		entityID = "newcom-esp"
	}

	keyID := os.Getenv("CRYPTO_MESH_KEY_ID")
	if keyID == "" {
		keyID = fmt.Sprintf("key_%s_v1", entityID)
	}

	creds := cryptomesh.ClientCredentials{
		EntityID:         entityID,
		TenantID:         tenantID,
		KeyID:            keyID,
		IngressSecretHex: ingressHex,
		EgressSecretHex:  egressHex,
		AuthSecretHex:    authHex,
	}

	client, err := cryptomesh.NewCryptoMeshClient(creds)
	if err != nil {
		log.Printf("[CryptoMesh] Failed to initialize client: %v\n", err)
		return
	}

	globalCryptoMeshClient = client
	log.Printf("[CryptoMesh] Successfully initialized for entity '%s' (Tenant: %s) with banking-grade AES-256-GCM + HMAC-SHA-512.\n", entityID, tenantID)
}

// InboundTransactionalEmailPayload represents the decrypted instruction sent by erp-1 or Communicator via NeuraCore.
type InboundTransactionalEmailPayload struct {
	Action       string                 `json:"action"` // e.g. "send_transactional_email", "order_confirmation"
	To           string                 `json:"to"`
	Subject      string                 `json:"subject"`
	HtmlBody     string                 `json:"htmlBody,omitempty"`
	TextBody     string                 `json:"textBody,omitempty"`
	TemplateID   string                 `json:"templateId,omitempty"`
	TemplateData map[string]interface{} `json:"templateData,omitempty"`
	OrderID      string                 `json:"orderId,omitempty"`
	CustomerID   string                 `json:"customerId,omitempty"`
}

// handleCryptoMeshIngress handles inbound encrypted envelopes relayed from NeuraCore.
// NeuraCore holds the dual-key translation: sender's message was decrypted with sender's key,
// audited, and re-encrypted with newcom/esp's egress key.
func handleCryptoMeshIngress(w http.ResponseWriter, r *http.Request) {
	if globalCryptoMeshClient == nil {
		http.Error(w, "CryptoMesh service is not enabled on this node", http.StatusServiceUnavailable)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var envelope cryptomesh.EncryptedTransitEnvelope
	if err := json.Unmarshal(bodyBytes, &envelope); err != nil {
		http.Error(w, "Invalid encrypted transit envelope JSON format", http.StatusBadRequest)
		return
	}

	var payload InboundTransactionalEmailPayload
	if err := globalCryptoMeshClient.DecryptFromRelayJSON(&envelope, &payload); err != nil {
		log.Printf("[CryptoMesh] Inbound decryption failed from sender '%s': %v\n", envelope.SenderID, err)
		http.Error(w, fmt.Sprintf("Cryptographic transit rejection: %v", err), http.StatusForbidden)
		return
	}

	log.Printf("[CryptoMesh] Authenticated transit message from '%s' for action '%s' (Order: %s, To: %s)\n",
		envelope.SenderID, payload.Action, payload.OrderID, payload.To)

	// Process action: queue transactional email dispatch
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"status":    "QUEUED",
		"sender":    envelope.SenderID,
		"action":    payload.Action,
		"recipient": payload.To,
	})
}

// SendEncryptedEcosystemEvent sends an outbound commercial event to another ecosystem service
// via NeuraCore's sovereign re-encryption relay gateway.
func SendEncryptedEcosystemEvent(recipientID string, payload interface{}) (*cryptomesh.EncryptedTransitEnvelope, error) {
	if globalCryptoMeshClient == nil {
		return nil, fmt.Errorf("CryptoMesh client is not initialized")
	}

	relayURL := os.Getenv("NEURACORE_RELAY_URL")
	if relayURL == "" {
		relayURL = "http://localhost:4000"
	}

	// 1. Encrypt with newcom-esp Ingress Key
	env, err := globalCryptoMeshClient.EncryptForRelay(recipientID, payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt envelope: %w", err)
	}

	// 2. Transmit through NeuraCore Relay
	return globalCryptoMeshClient.TransmitThroughRelay(relayURL, env)
}
