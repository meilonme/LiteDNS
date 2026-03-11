package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
)

const masterKeyEnv = "LITEDNS_MASTER_KEY"

func LoadMasterKey() ([]byte, error) {
	raw := os.Getenv(masterKeyEnv)
	if raw == "" {
		return nil, fmt.Errorf("%s is required", masterKeyEnv)
	}

	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", masterKeyEnv, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s must decode to 32 bytes", masterKeyEnv)
	}
	return key, nil
}

func EncryptSecret(masterKey []byte, plaintext string) (string, error) {
	if plaintext == "" {
		return "", fmt.Errorf("encrypt secret: empty plaintext")
	}

	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return "", fmt.Errorf("encrypt secret: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("encrypt secret: new gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("encrypt secret: nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	blob := append(nonce, ciphertext...)
	return base64.StdEncoding.EncodeToString(blob), nil
}

func DecryptSecret(masterKey []byte, encoded string) (string, error) {
	blob, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: decode: %w", err)
	}

	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: new gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(blob) < nonceSize {
		return "", fmt.Errorf("decrypt secret: invalid blob")
	}
	nonce := blob[:nonceSize]
	ciphertext := blob[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: open: %w", err)
	}
	return string(plaintext), nil
}
