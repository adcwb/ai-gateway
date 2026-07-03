package pkg

import (
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	plain := "sk-real-upstream-key-AbCdEf123456"

	enc, err := EncryptAES(plain, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc == plain || strings.Contains(enc, plain) {
		t.Fatal("ciphertext must not contain the plaintext")
	}
	dec, err := DecryptAES(enc, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if dec != plain {
		t.Fatalf("round trip mismatch: got %q", dec)
	}
}

func TestEncryptProducesUniqueCiphertexts(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	a, _ := EncryptAES("same", key)
	b, _ := EncryptAES("same", key)
	if a == b {
		t.Fatal("nonce reuse: two encryptions of the same plaintext were identical")
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	enc, _ := EncryptAES("secret", []byte("0123456789abcdef0123456789abcdef"))
	if _, err := DecryptAES(enc, []byte("ffffffffffffffffffffffffffffffff")); err == nil {
		t.Fatal("decrypting with the wrong key must fail")
	}
}

func TestShortKeyIsNormalized(t *testing.T) {
	short := []byte("short-key")
	enc, err := EncryptAES("v", short)
	if err != nil {
		t.Fatalf("encrypt with short key: %v", err)
	}
	dec, err := DecryptAES(enc, short)
	if err != nil || dec != "v" {
		t.Fatalf("round trip with short key failed: %v %q", err, dec)
	}
}
