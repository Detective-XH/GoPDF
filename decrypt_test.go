package pdf

import "testing"

// TestValidAESKeyLen locks the AES key-length invariant: only 16/24/32-byte
// keys (128/192/256-bit) are accepted. Any other length — including the 5-byte
// key a crafted /Length 40 + AESV2 file would derive — must be rejected.
func TestValidAESKeyLen(t *testing.T) {
	valid := map[int]bool{16: true, 24: true, 32: true}
	for n := 0; n <= 64; n++ {
		if got := validAESKeyLen(n); got != valid[n] {
			t.Errorf("validAESKeyLen(%d) = %v, want %v", n, got, valid[n])
		}
	}
}

// TestDecryptAESRejectsBadKeyLen confirms decryptAES screens an invalid-length
// key (e.g. the short per-object key cryptKey derives from a crafted /Length 40
// file) before constructing the cipher, degrading to nil rather than panicking.
func TestDecryptAESRejectsBadKeyLen(t *testing.T) {
	data := make([]byte, 2*16) // one IV block + one ciphertext block
	for _, kl := range []int{0, 5, 10, 15, 17, 31, 33} {
		if got := decryptAES(make([]byte, kl), data); got != nil {
			t.Errorf("decryptAES with %d-byte key returned %v, want nil", kl, got)
		}
	}
}
