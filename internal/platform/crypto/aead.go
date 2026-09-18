package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
)

// AEAD encrypts and decrypts small secrets (integration credentials, GST
// API tokens, WhatsApp/SMTP secrets) at rest using AES-256-GCM. The key is
// expected to come from environment/secrets management (brief §60) —
// never from a database column and never logged.
type AEAD struct {
	gcm cipher.AEAD
}

// NewAEAD builds an AEAD from a 32-byte (AES-256) key.
func NewAEAD(key []byte) (*AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: AEAD key must be 32 bytes (AES-256), got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: building AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: building GCM: %w", err)
	}
	return &AEAD{gcm: gcm}, nil
}

// Seal encrypts plaintext, returning nonce||ciphertext||tag as a single
// byte slice. additionalData is authenticated but not encrypted (e.g. a
// record ID, to bind the ciphertext to the row it belongs to and detect a
// ciphertext copied between rows).
func (a *AEAD) Seal(plaintext, additionalData []byte) ([]byte, error) {
	nonce := make([]byte, a.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: generating nonce: %w", err)
	}
	return a.gcm.Seal(nonce, nonce, plaintext, additionalData), nil
}

// Open decrypts a value produced by Seal. additionalData must match what
// was passed to Seal.
func (a *AEAD) Open(sealed, additionalData []byte) ([]byte, error) {
	nonceSize := a.gcm.NonceSize()
	if len(sealed) < nonceSize {
		return nil, fmt.Errorf("crypto: sealed value shorter than nonce size")
	}
	nonce, ciphertext := sealed[:nonceSize], sealed[nonceSize:]
	plaintext, err := a.gcm.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, fmt.Errorf("crypto: decryption failed: %w", err)
	}
	return plaintext, nil
}

// LoadOrGenerateAEADKey reads a base64-encoded 32-byte key from
// AEAD_ENCRYPTION_KEY — shared by apps/server and apps/worker's
// composition roots, both of which need the identical key (apps/server
// encrypts einvoice provider credentials via Seal, apps/worker decrypts
// them via Open; different keys would silently make every credential
// undecryptable). In production this must be set from secrets management
// (brief §60) — generating an ephemeral key is only acceptable for local
// development, where losing already-encrypted secrets on restart is a
// non-issue, and this path logs loudly so it's never silently relied on
// in a real deployment.
func LoadOrGenerateAEADKey(logger *slog.Logger) ([]byte, error) {
	if encoded := os.Getenv("AEAD_ENCRYPTION_KEY"); encoded != "" {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, errors.New("config: AEAD_ENCRYPTION_KEY is not valid base64")
		}
		if len(key) != 32 {
			return nil, errors.New("config: AEAD_ENCRYPTION_KEY must decode to exactly 32 bytes")
		}
		return key, nil
	}
	logger.Warn("AEAD_ENCRYPTION_KEY not set — generating an EPHEMERAL key for this process only. " +
		"Any MFA secret or einvoice provider credential encrypted with it becomes unreadable on " +
		"restart (or unreadable to a sibling process using a different ephemeral key). Set " +
		"AEAD_ENCRYPTION_KEY (32 random bytes, base64-encoded) before running this in production.")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}
