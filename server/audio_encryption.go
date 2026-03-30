package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

const AudioEncryptedType = "EncryptedAES256GCM"

// EncryptAudio encrypts raw audio bytes using AES-256-GCM with the provided key.
// Returns base64(nonce || ciphertext) suitable for embedding in the JSON payload.
// The nonce is randomly generated for each call (12 bytes for GCM).
func EncryptAudio(key, audio []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("audio_encryption: cipher init: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("audio_encryption: gcm init: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("audio_encryption: nonce gen: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, audio, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptAudio reverses EncryptAudio — used in tests and downstream validation.
func DecryptAudio(key []byte, b64Ciphertext string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(b64Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("audio_encryption: base64 decode: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("audio_encryption: cipher init: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("audio_encryption: gcm init: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("audio_encryption: ciphertext too short")
	}
	nonce, ct := data[:nonceSize], data[nonceSize:]
	return gcm.Open(nil, nonce, ct, nil)
}

// keyExchangeRequest mirrors the relay server's KeyExchangeRequest struct.
type keyExchangeRequest struct {
	PublicKey string `json:"public_key"`
}

// keyExchangeResponse mirrors the relay server's KeyExchangeResponse struct.
type keyExchangeResponse struct {
	PublicKey  string `json:"public_key"`
	WrappedKey string `json:"wrapped_key"`
}

// FetchAudioKeyFromRelay performs an ECDH P-256 key exchange with the relay
// server's /api/audio/key-exchange endpoint and returns the unwrapped 32-byte
// AES-256-GCM master key. The raw key is never transmitted — it arrives wrapped
// in an ECDH-derived AES-GCM envelope.
func FetchAudioKeyFromRelay(relayURL, apiKey string) ([]byte, error) {
	// Generate an ephemeral ECDH P-256 key pair for this request.
	privKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("audio_encryption: ecdh keygen: %w", err)
	}

	pubKeyB64 := base64.StdEncoding.EncodeToString(privKey.PublicKey().Bytes())

	reqBody, err := json.Marshal(keyExchangeRequest{PublicKey: pubKeyB64})
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(relayURL, "/") + "/api/audio/key-exchange"

	httpClient := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("audio_encryption: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("audio_encryption: relay request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("audio_encryption: relay returned %d: %s", resp.StatusCode, string(body))
	}

	var kxResp keyExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&kxResp); err != nil {
		return nil, fmt.Errorf("audio_encryption: decode response: %w", err)
	}

	// Decode relay server's ephemeral public key.
	serverPubKeyBytes, err := base64.StdEncoding.DecodeString(kxResp.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("audio_encryption: decode server pubkey: %w", err)
	}
	serverPubKey, err := ecdh.P256().NewPublicKey(serverPubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("audio_encryption: parse server pubkey: %w", err)
	}

	// Compute the same ECDH shared secret.
	sharedSecret, err := privKey.ECDH(serverPubKey)
	if err != nil {
		return nil, fmt.Errorf("audio_encryption: ecdh shared secret: %w", err)
	}

	// Derive the wrapping key using the same HKDF parameters as the relay server.
	hkdfReader := hkdf.New(sha256.New, sharedSecret, nil, []byte("tlr-audio-key-wrap-v1"))
	wrappingKey := make([]byte, 32)
	if _, err := io.ReadFull(hkdfReader, wrappingKey); err != nil {
		return nil, fmt.Errorf("audio_encryption: hkdf: %w", err)
	}

	// Unwrap (decrypt) the master AES key.
	wrappedKeyBytes, err := base64.StdEncoding.DecodeString(kxResp.WrappedKey)
	if err != nil {
		return nil, fmt.Errorf("audio_encryption: decode wrapped key: %w", err)
	}

	block, err := aes.NewCipher(wrappingKey)
	if err != nil {
		return nil, fmt.Errorf("audio_encryption: unwrap cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("audio_encryption: unwrap gcm: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(wrappedKeyBytes) < nonceSize {
		return nil, fmt.Errorf("audio_encryption: wrapped key too short")
	}
	masterKey, err := gcm.Open(nil, wrappedKeyBytes[:nonceSize], wrappedKeyBytes[nonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("audio_encryption: unwrap decrypt: %w", err)
	}
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("audio_encryption: expected 32-byte master key, got %d", len(masterKey))
	}

	return masterKey, nil
}
