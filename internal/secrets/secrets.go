// Package secrets provides authenticated symmetric encryption (AES-256-GCM)
// for values that must not live as plaintext in the SQLite database — most
// notably the hot-reloaded IS24 session cookie.
//
// Key handling, in priority order:
//
//  1. SECRETS_KEY env var, base64-encoded 32 bytes. Preferred for production
//     because the key lives outside the data volume.
//  2. <dataDir>/.secrets-key, a freshly generated 32-byte key written with
//     0600 perms. Convenient fallback so the feature works out of the box;
//     the file shares a fate with the DB backup, so it only defends against
//     leakage of the DB without the key file.
//
// Ciphertext envelope: "enc:v1:" + base64( nonce(12) || ciphertext || tag ).
// The visible prefix lets us detect legacy plaintext on read and migrate it.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// envelopePrefix marks an encrypted value so callers can distinguish it from
// a legacy plaintext meta row and migrate transparently.
const envelopePrefix = "enc:v1:"

// keyLen is the AES-256 key size in bytes.
const keyLen = 32

// EnvVar is the env var checked for an externally-supplied key.
const EnvVar = "SECRETS_KEY"

// KeyFileName is the file written under dataDir when no env key is supplied.
const KeyFileName = ".secrets-key"

// Encrypter performs round-trip encryption with a single AES-256-GCM key.
// Safe for concurrent use — the underlying cipher.AEAD is stateless after
// construction; nonces are random per call.
type Encrypter struct {
	gcm cipher.AEAD
}

// LoadOrGenerateKey resolves the key per the package doc: env > file. When it
// generates a fresh file, it logs the path so the operator notices it. The
// returned key is exactly 32 bytes.
func LoadOrGenerateKey(dataDir string, logger *slog.Logger) ([]byte, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if v := strings.TrimSpace(os.Getenv(EnvVar)); v != "" {
		key, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", EnvVar, err)
		}
		if len(key) != keyLen {
			return nil, fmt.Errorf("%s must decode to %d bytes, got %d", EnvVar, keyLen, len(key))
		}
		logger.Info("secrets key loaded from env", "source", EnvVar)
		return key, nil
	}

	path := filepath.Join(dataDir, KeyFileName)
	if b, err := os.ReadFile(path); err == nil {
		if len(b) != keyLen {
			return nil, fmt.Errorf("%s: expected %d bytes, got %d (delete to regenerate)", path, keyLen, len(b))
		}
		return b, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	key := make([]byte, keyLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	if err := os.WriteFile(path, key, 0600); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	logger.Warn("generated new secrets key on disk — set SECRETS_KEY env to externalize", "path", path)
	return key, nil
}

// NewEncrypter wraps a 32-byte key in an AES-GCM AEAD ready for Encrypt/Decrypt.
func NewEncrypter(key []byte) (*Encrypter, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("key must be %d bytes, got %d", keyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	return &Encrypter{gcm: gcm}, nil
}

// Encrypt returns "enc:v1:" + base64(nonce || ciphertext || tag). Each call
// generates a fresh random nonce, so identical plaintexts produce distinct
// ciphertexts (avoids leaking equality across rows).
func (e *Encrypter) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}
	sealed := e.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return envelopePrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt undoes Encrypt. Returns ErrNotEncrypted when the input lacks the
// envelope prefix so the caller can decide whether to treat it as legacy
// plaintext or as corruption.
func (e *Encrypter) Decrypt(envelope string) (string, error) {
	if !IsEncrypted(envelope) {
		return "", ErrNotEncrypted
	}
	raw, err := base64.StdEncoding.DecodeString(envelope[len(envelopePrefix):])
	if err != nil {
		return "", fmt.Errorf("decode envelope: %w", err)
	}
	ns := e.gcm.NonceSize()
	if len(raw) < ns {
		return "", errors.New("envelope too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := e.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	return string(pt), nil
}

// IsEncrypted reports whether the value carries the envelope prefix and is
// therefore something Decrypt can handle (modulo key mismatch).
func IsEncrypted(s string) bool { return strings.HasPrefix(s, envelopePrefix) }

// ErrNotEncrypted is returned by Decrypt when its input has no envelope. It is
// useful to detect legacy plaintext rows that predate this feature.
var ErrNotEncrypted = errors.New("value is not encrypted")
