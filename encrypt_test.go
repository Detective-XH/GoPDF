package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"testing"
)

// aesCBCEncrypt returns [IV || PKCS7-padded ciphertext] for plaintext using key.
func aesCBCEncrypt(key, plaintext []byte) []byte {
	block, _ := aes.NewCipher(key)
	// PKCS7 pad
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(plaintext, bytes.Repeat([]byte{byte(pad)}, pad)...)
	iv := make([]byte, aes.BlockSize)
	_, _ = rand.Read(iv)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	return append(iv, ct...)
}

// --- decryptAES -----------------------------------------------------------

// TestDecryptAESRoundtrip encrypts a known plaintext and verifies decryptAES
// recovers it exactly.
func TestDecryptAESRoundtrip(t *testing.T) {
	key := make([]byte, 16)
	_, _ = rand.Read(key)
	want := []byte("hello, world!")

	data := aesCBCEncrypt(key, want)
	got := decryptAES(key, data)
	if !bytes.Equal(got, want) {
		t.Errorf("roundtrip: got %q, want %q", got, want)
	}
}

// TestDecryptAESTooShort verifies that payloads shorter than one BlockSize
// (not even a full IV) return nil.
func TestDecryptAESTooShort(t *testing.T) {
	key := make([]byte, 16)
	if got := decryptAES(key, make([]byte, aes.BlockSize-1)); got != nil {
		t.Errorf("too-short: want nil, got %v", got)
	}
}

// TestDecryptAESExactlyOneBlock verifies that a payload of exactly one
// BlockSize (all IV, zero ciphertext bytes) returns nil.
func TestDecryptAESExactlyOneBlock(t *testing.T) {
	key := make([]byte, 16)
	if got := decryptAES(key, make([]byte, aes.BlockSize)); got != nil {
		t.Errorf("iv-only: want nil, got %v", got)
	}
}

// TestDecryptAESBadCiphertextLength verifies that a ciphertext whose length
// after the IV is not a multiple of BlockSize returns nil.
func TestDecryptAESBadCiphertextLength(t *testing.T) {
	key := make([]byte, 16)
	// aes.BlockSize + 1 byte of "ciphertext" — not a valid block multiple.
	if got := decryptAES(key, make([]byte, aes.BlockSize+1)); got != nil {
		t.Errorf("bad-ct-len: want nil, got %v", got)
	}
}

// TestDecryptAESBadPadding verifies that corrupted PKCS7 padding returns nil.
func TestDecryptAESBadPadding(t *testing.T) {
	key := make([]byte, 16)
	// Encrypt valid plaintext, then corrupt the last byte of the ciphertext so
	// that the padding byte decrypts to an invalid value.
	data := aesCBCEncrypt(key, []byte("test"))
	data[len(data)-1] ^= 0xFF // flip last byte → bad padding
	if got := decryptAES(key, data); got != nil {
		t.Errorf("bad-padding: want nil, got %q", got)
	}
}

// TestDecryptAESBadKey verifies that a key of unsupported length returns nil
// (aes.NewCipher rejects it).
func TestDecryptAESBadKey(t *testing.T) {
	badKey := make([]byte, 7) // not 16, 24, or 32
	data := make([]byte, aes.BlockSize*2)
	if got := decryptAES(badKey, data); got != nil {
		t.Errorf("bad-key: want nil, got %v", got)
	}
}

// TestDecryptAESEmptyPlaintext verifies that valid empty plaintext returns a
// non-nil empty slice (not nil), distinguishing "success with no bytes" from
// "validation failure".
func TestDecryptAESEmptyPlaintext(t *testing.T) {
	key := make([]byte, 16)
	_, _ = rand.Read(key)
	data := aesCBCEncrypt(key, []byte{})
	got := decryptAES(key, data)
	if got == nil {
		t.Error("empty-plaintext: want non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("empty-plaintext: want len 0, got %d", len(got))
	}
}
