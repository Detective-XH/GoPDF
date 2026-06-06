package pdf

import (
	"crypto/md5"
	"crypto/rc4"
	"crypto/subtle"
	"fmt"
)

var passwordPad = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80, 0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

// parseEncryptBody extracts and validates n, O, U, P, and ID from the
// Standard encryption dictionary and the file trailer.
func parseEncryptBody(encrypt dict, trailer dict) (n int64, O, U string, P uint32, ID []byte, err error) {
	if encrypt["Filter"] != name("Standard") {
		return 0, "", "", 0, nil, fmt.Errorf("unsupported PDF: encryption filter %v", objfmt(encrypt["Filter"]))
	}
	n, err = validateKeyLength(encrypt)
	if err != nil {
		return 0, "", "", 0, nil, err
	}
	ID, err = extractTrailerID(trailer)
	if err != nil {
		return 0, "", "", 0, nil, err
	}
	O, _ = encrypt["O"].(string)
	U, _ = encrypt["U"].(string)
	if len(O) != 32 || len(U) != 32 {
		return 0, "", "", 0, nil, fmt.Errorf("malformed PDF: missing O= or U= encryption parameters")
	}
	p, _ := encrypt["P"].(int64)
	return n, O, U, uint32(p), ID, nil
}

// validateKeyLength reads and validates the Length field of the encrypt dict,
// defaulting to 40 when absent.
func validateKeyLength(encrypt dict) (int64, error) {
	n, _ := encrypt["Length"].(int64)
	if n == 0 {
		n = 40
	}
	if n%8 != 0 || n > 128 || n < 40 {
		return 0, fmt.Errorf("malformed PDF: %d-bit encryption key", n)
	}
	return n, nil
}

// extractTrailerID retrieves the first element of the ID array from the trailer.
func extractTrailerID(trailer dict) ([]byte, error) {
	ids, ok := trailer["ID"].(array)
	if !ok || len(ids) < 1 {
		return nil, fmt.Errorf("malformed PDF: missing ID in trailer")
	}
	idstr, ok := ids[0].(string)
	if !ok {
		return nil, fmt.Errorf("malformed PDF: missing ID in trailer")
	}
	return []byte(idstr), nil
}

// parseEncryptHeader validates the V version and R revision of the encrypt dict.
// Crypt-filter validation for V=4 lives in resolveCryptFilters.
func parseEncryptHeader(encrypt dict) (V, R int64, err error) {
	V, _ = encrypt["V"].(int64)
	if V != 1 && V != 2 && V != 4 {
		return 0, 0, fmt.Errorf("unsupported PDF: encryption version V=%d; %v", V, objfmt(encrypt))
	}
	R, _ = encrypt["R"].(int64)
	if R < 2 {
		return 0, 0, fmt.Errorf("malformed PDF: encryption revision R=%d", R)
	}
	if R > 4 {
		return 0, 0, fmt.Errorf("unsupported PDF: encryption revision R=%d", R)
	}
	return V, R, nil
}

// padPassword returns the password padded or truncated to exactly 32 bytes
// (PDF 32000-1:2008 §7.6.3.3 Algorithm 2 step a).
func padPassword(password string) []byte {
	pw := []byte(password)
	if len(pw) >= 32 {
		return pw[:32]
	}
	return append(pw, passwordPad[:32-len(pw)]...)
}

// buildEncryptKey computes the RC4 encryption key and cipher from the given
// PDF Standard encryption parameters (PDF 32000-1:2008 §7.6.3.3).
func buildEncryptKey(password, O string, P uint32, n, R int64, ID []byte, encryptMetadata bool) ([]byte, *rc4.Cipher, error) {
	h := md5.New()
	h.Write(padPassword(password))
	h.Write([]byte(O))
	h.Write([]byte{byte(P), byte(P >> 8), byte(P >> 16), byte(P >> 24)})
	h.Write(ID)
	if R >= 4 && !encryptMetadata {
		// ISO 32000-1 §7.6.3.3 Algorithm 2 step (f): revision 4 or greater.
		// R=2/R=3 predate /EncryptMetadata and must not get this step.
		h.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	}
	key := h.Sum(nil)
	if R >= 3 {
		for range 50 {
			h.Reset()
			h.Write(key[:n/8])
			key = h.Sum(key[:0])
		}
		key = key[:n/8]
	} else {
		key = key[:40/8]
	}
	c, err := rc4.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("malformed PDF: invalid RC4 key: %v", err)
	}
	return key, c, nil
}

// verifyEncryptKey checks the computed key against the U entry in the
// encryption dictionary (PDF 32000-1:2008 §7.6.3.4).
func verifyEncryptKey(R int64, key []byte, c *rc4.Cipher, U string, ID []byte) bool {
	var u []byte
	if R == 2 {
		u = make([]byte, 32)
		copy(u, passwordPad)
		c.XORKeyStream(u, u)
	} else {
		h := md5.New()
		h.Write(passwordPad)
		h.Write(ID)
		u = h.Sum(nil)
		c.XORKeyStream(u, u)
		for i := 1; i <= 19; i++ {
			key1 := make([]byte, len(key))
			copy(key1, key)
			for j := range key1 {
				key1[j] ^= byte(i)
			}
			c, _ = rc4.NewCipher(key1)
			c.XORKeyStream(u, u)
		}
	}
	// Constant-time comparison avoids timing side channels in password checks.
	return subtle.ConstantTimeCompare([]byte(U)[:len(u)], u) == 1
}

// ownerEncryptKey derives the RC4 key that encrypts the /O entry from the
// owner password (PDF 32000-1:2008 §7.6.3.4 Algorithm 3 steps a-d). The 50
// MD5 iterations hash only the first n/8 digest bytes, matching the
// Algorithm 2 loop in buildEncryptKey: Adobe's wording for step (c) omits
// the truncation, but producers (Ghostscript, qpdf) apply it, and the
// difference is observable only when n < 128 - verified against the
// Ghostscript-generated R3/40 fixture in testdata/encrypted.
func ownerEncryptKey(owner string, n, R int64) []byte {
	h := md5.New()
	h.Write(padPassword(owner))
	key := h.Sum(nil)
	if R < 3 {
		return key[:40/8]
	}
	for range 50 {
		h.Reset()
		h.Write(key[:n/8])
		key = h.Sum(key[:0])
	}
	return key[:n/8]
}

// userPassFromOwner recovers the padded user password from the /O entry
// given the owner password (PDF 32000-1:2008 §7.6.3.4 Algorithm 7 steps a-b).
func userPassFromOwner(owner, O string, n, R int64) string {
	key := ownerEncryptKey(owner, n, R)
	buf := []byte(O)
	if R == 2 {
		c, _ := rc4.NewCipher(key)
		c.XORKeyStream(buf, buf)
		return string(buf)
	}
	for i := 19; i >= 0; i-- {
		key1 := make([]byte, len(key))
		for j := range key {
			key1[j] = key[j] ^ byte(i)
		}
		c, _ := rc4.NewCipher(key1)
		c.XORKeyStream(buf, buf)
	}
	return string(buf)
}

// ownerAuthKey authenticates password as the owner password (PDF 32000-1:2008
// §7.6.3.4 Algorithm 7): it recovers the user password from /O, then runs
// user authentication with it. It returns the file encryption key, or
// ErrInvalidPassword when the recovered user password fails against /U.
func ownerAuthKey(password, O, U string, P uint32, n, R int64, ID []byte, encryptMetadata bool) ([]byte, error) {
	userPw := userPassFromOwner(password, O, n, R)
	key, c, err := buildEncryptKey(userPw, O, P, n, R, ID, encryptMetadata)
	if err != nil {
		return nil, err
	}
	if !verifyEncryptKey(R, key, c, U, ID) {
		return nil, ErrInvalidPassword
	}
	return key, nil
}
