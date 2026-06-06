package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, keyLen)
	for i := range key {
		key[i] = byte(i)
	}
	enc, err := NewEncrypter(key)
	if err != nil {
		t.Fatalf("NewEncrypter: %v", err)
	}
	const pt = "cookie=abc123; sessionid=xyz; very long string with spaces and ünïcödé"
	ct, err := enc.Encrypt(pt)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !IsEncrypted(ct) {
		t.Fatalf("ciphertext missing envelope prefix: %q", ct)
	}
	if strings.Contains(ct, pt) {
		t.Fatalf("ciphertext leaks plaintext")
	}
	got, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != pt {
		t.Fatalf("round-trip mismatch: got %q want %q", got, pt)
	}
}

func TestEncryptProducesDistinctCiphertexts(t *testing.T) {
	key := make([]byte, keyLen)
	enc, _ := NewEncrypter(key)
	a, _ := enc.Encrypt("same input")
	b, _ := enc.Encrypt("same input")
	if a == b {
		t.Fatalf("expected distinct ciphertexts due to random nonce, got identical: %q", a)
	}
}

func TestDecryptRejectsPlaintext(t *testing.T) {
	key := make([]byte, keyLen)
	enc, _ := NewEncrypter(key)
	if _, err := enc.Decrypt("plain cookie value"); err != ErrNotEncrypted {
		t.Fatalf("expected ErrNotEncrypted, got %v", err)
	}
}

func TestDecryptRejectsTampered(t *testing.T) {
	key := make([]byte, keyLen)
	enc, _ := NewEncrypter(key)
	ct, _ := enc.Encrypt("hello")
	tampered := ct[:len(ct)-2] + "AA"
	if _, err := enc.Decrypt(tampered); err == nil {
		t.Fatalf("expected error from tampered ciphertext")
	}
}

func TestDecryptRejectsWrongKey(t *testing.T) {
	a := make([]byte, keyLen)
	b := make([]byte, keyLen)
	for i := range b {
		b[i] = 1
	}
	encA, _ := NewEncrypter(a)
	encB, _ := NewEncrypter(b)
	ct, _ := encA.Encrypt("hello")
	if _, err := encB.Decrypt(ct); err == nil {
		t.Fatalf("expected error decrypting with wrong key")
	}
}

func TestLoadOrGenerateKey_FromEnv(t *testing.T) {
	t.Setenv(EnvVar, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=") // 32 zero bytes base64
	dir := t.TempDir()
	key, err := LoadOrGenerateKey(dir, nil)
	if err != nil {
		t.Fatalf("LoadOrGenerateKey: %v", err)
	}
	if len(key) != keyLen {
		t.Fatalf("len(key) = %d, want %d", len(key), keyLen)
	}
	if _, err := os.Stat(filepath.Join(dir, KeyFileName)); !os.IsNotExist(err) {
		t.Fatalf("expected no key file when env is set, got err=%v", err)
	}
}

func TestLoadOrGenerateKey_FromFile(t *testing.T) {
	t.Setenv(EnvVar, "")
	dir := t.TempDir()
	key1, err := LoadOrGenerateKey(dir, nil)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	st, err := os.Stat(filepath.Join(dir, KeyFileName))
	if err != nil {
		t.Fatalf("key file missing: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0600 {
		t.Fatalf("key file perms = %v, want 0600", perm)
	}
	key2, err := LoadOrGenerateKey(dir, nil)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if string(key1) != string(key2) {
		t.Fatalf("expected stable key across reads")
	}
}

func TestLoadOrGenerateKey_RejectsBadEnv(t *testing.T) {
	t.Setenv(EnvVar, "not valid base64 !!!")
	dir := t.TempDir()
	if _, err := LoadOrGenerateKey(dir, nil); err == nil {
		t.Fatalf("expected error on bad env value")
	}
}

func TestLoadOrGenerateKey_RejectsShortEnv(t *testing.T) {
	t.Setenv(EnvVar, "c2hvcnQ=") // "short"
	dir := t.TempDir()
	if _, err := LoadOrGenerateKey(dir, nil); err == nil {
		t.Fatalf("expected error on short key")
	}
}
