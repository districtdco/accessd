package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const algorithmAES256GCM = "AES-256-GCM"

type Cipher struct {
	key   []byte
	keyID string
}

func NewCipher(masterKey, keyID string) (*Cipher, error) {
	resolvedKeyID := strings.TrimSpace(keyID)
	if resolvedKeyID == "" {
		resolvedKeyID = "v1"
	}

	key, err := decodeOrDeriveKey(masterKey)
	if err != nil {
		return nil, err
	}

	return &Cipher{key: key, keyID: resolvedKeyID}, nil
}

func (c *Cipher) KeyID() string {
	return c.keyID
}

func (c *Cipher) Encrypt(plaintext, aad []byte) (nonce []byte, encrypted []byte, err error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, nil, fmt.Errorf("create aes cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("create gcm: %w", err)
	}

	nonce = make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("generate nonce: %w", err)
	}

	encrypted = aead.Seal(nil, nonce, plaintext, aad)
	return nonce, encrypted, nil
}

func (c *Cipher) Decrypt(nonce, encrypted, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("invalid nonce size")
	}

	plaintext, err := aead.Open(nil, nonce, encrypted, aad)
	if err != nil {
		return nil, fmt.Errorf("decrypt credential secret: %w", err)
	}

	return plaintext, nil
}

func decodeOrDeriveKey(masterKey string) ([]byte, error) {
	trimmed := strings.TrimSpace(masterKey)
	if trimmed == "" {
		return nil, fmt.Errorf("ACCESSD_VAULT_KEY is required")
	}

	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		if len(decoded) != 32 {
			return nil, fmt.Errorf("ACCESSD_VAULT_KEY base64 value must decode to 32 bytes")
		}
		return decoded, nil
	}

	hash := sha256.Sum256([]byte(trimmed))
	return hash[:], nil
}
