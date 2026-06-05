// AES-256 (V=5, R=5/R=6) Standard security handler key derivation.
// Algorithms 2.A and 2.B of ISO 32000-2 §7.6.4.3. Clean-room from the ISO spec,
// cross-checked against Apache-2.0 pdfcpu and qpdf. NOT derived from AGPL unipdf.

package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"fmt"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// initEncryptAES256 handles V=5 (R=5 / R=6). On success it sets r.key (the
// 32-byte file encryption key), r.stmMode/r.strMode, and r.encryptMetadata.
func (r *Reader) initEncryptAES256(encrypt dict, password string) error {
	if encrypt["Filter"] != name("Standard") {
		return fmt.Errorf("unsupported PDF: encryption filter %v", objfmt(encrypt["Filter"]))
	}
	rev, _ := encrypt["R"].(int64)
	if rev != 5 && rev != 6 {
		return fmt.Errorf("unsupported PDF: AES-256 revision R=%d", rev)
	}
	stm, str, err := resolveCryptFilters(encrypt, 5)
	if err != nil {
		return err
	}
	O, _ := encrypt["O"].(string)
	U, _ := encrypt["U"].(string)
	OE, _ := encrypt["OE"].(string)
	UE, _ := encrypt["UE"].(string)
	// Exact lengths per ISO 32000-2 §7.6.4.3 (O/U=48, OE/UE=32), mirroring the
	// exact-length check parseEncryptBody applies to the V<=4 O/U fields.
	if len(O) != 48 || len(U) != 48 || len(OE) != 32 || len(UE) != 32 {
		return fmt.Errorf("malformed PDF: AES-256 O/U/OE/UE length")
	}
	key := validateAES256Password(rev, saslPrep(password),
		[]byte(O), []byte(U), []byte(OE), []byte(UE))
	if key == nil {
		return ErrInvalidPassword
	}
	r.key = key
	r.stmMode, r.strMode = stm, str
	// /EncryptMetadata has no key-derivation impact at V=5 (ISO 32000-2); it
	// only controls the metadata-stream decryption skip.
	r.encryptMetadata = encryptMetadataFlag(encrypt)
	return nil
}

// validateAES256Password tries the user password path, then the owner path
// (which appends the 48-byte U as udata). Returns the 32-byte file key or nil.
// The hash comparison uses subtle.ConstantTimeCompare to match the timing
// side-channel guard already applied in verifyEncryptKey (encrypt.go).
func validateAES256Password(rev int64, pw, O, U, OE, UE []byte) []byte {
	if subtle.ConstantTimeCompare(hashAES256(rev, pw, U[32:40], nil), U[:32]) == 1 {
		return aesCBCNoPad(hashAES256(rev, pw, U[40:48], nil), UE)
	}
	if subtle.ConstantTimeCompare(hashAES256(rev, pw, O[32:40], U), O[:32]) == 1 {
		return aesCBCNoPad(hashAES256(rev, pw, O[40:48], U), OE)
	}
	return nil
}

// hashAES256 is the revision hash H(): SHA-256 for R5, Algorithm 2.B for R6.
func hashAES256(rev int64, password, salt, udata []byte) []byte {
	if rev == 5 {
		s := sha256.Sum256(concat(password, salt, udata))
		return s[:]
	}
	return hash2B(password, salt, udata)
}

// hash2B implements ISO 32000-2 Algorithm 2.B (revision 6).
func hash2B(password, salt, udata []byte) []byte {
	sum := sha256.Sum256(concat(password, salt, udata))
	k := sum[:]
	for round := 1; ; round++ {
		k1 := bytes.Repeat(concat(password, k, udata), 64)
		block, err := aes.NewCipher(k[:16])
		if err != nil {
			return nil
		}
		e := make([]byte, len(k1))
		cipher.NewCBCEncrypter(block, k[16:32]).CryptBlocks(e, k1)
		mod := 0
		for _, b := range e[:16] {
			mod += int(b)
		}
		switch mod % 3 {
		case 0:
			s := sha256.Sum256(e)
			k = s[:]
		case 1:
			s := sha512.Sum384(e)
			k = s[:]
		case 2:
			s := sha512.Sum512(e)
			k = s[:]
		}
		if round >= 64 && int(e[len(e)-1]) <= round-32 {
			break
		}
	}
	return k[:32]
}

// aesCBCNoPad decrypts the 32-byte UE/OE key wrapper: AES-256-CBC, zero IV, no
// padding. Returns nil on bad key length or non-block-aligned data.
func aesCBCNoPad(key, data []byte) []byte {
	block, err := aes.NewCipher(key)
	if err != nil || len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(out, data)
	return out
}

// saslPrepMap maps a single rune per the RFC 3454 tables required by RFC 4013
// (SASLprep). Returns -1 to delete (B.1), the rune unchanged for most input,
// or U+0020 for non-ASCII spaces (C.1.2). U+200B appears in both tables; B.1
// wins (delete), matching libidn behaviour.
func saslPrepMap(r rune) rune {
	// RFC 3454 table B.1 — map to nothing (delete).
	switch r {
	case 0x00AD, // SOFT HYPHEN
		0x034F, // COMBINING GRAPHEME JOINER
		0x1806, // MONGOLIAN TODO SOFT HYPHEN
		0x180B, // MONGOLIAN FREE VARIATION SELECTOR ONE
		0x180C, // MONGOLIAN FREE VARIATION SELECTOR TWO
		0x180D, // MONGOLIAN FREE VARIATION SELECTOR THREE
		0x200B, // ZERO WIDTH SPACE (B.1 takes precedence over C.1.2)
		0x200C, // ZERO WIDTH NON-JOINER
		0x200D, // ZERO WIDTH JOINER
		0x2060, // WORD JOINER
		0xFEFF: // ZERO WIDTH NO-BREAK SPACE / BOM
		return -1
	}
	// FE00–FE0F: VARIATION SELECTORs (RFC 3454 table B.1).
	if r >= 0xFE00 && r <= 0xFE0F {
		return -1
	}
	// RFC 3454 table C.1.2 — non-ASCII space → U+0020.
	switch r {
	case 0x00A0, // NO-BREAK SPACE
		0x1680, // OGHAM SPACE MARK
		0x202F, // NARROW NO-BREAK SPACE
		0x205F, // MEDIUM MATHEMATICAL SPACE
		0x3000: // IDEOGRAPHIC SPACE
		return 0x0020
	}
	// U+2000–U+200A: EN QUAD … HAIR SPACE (C.1.2).
	if r >= 0x2000 && r <= 0x200A {
		return 0x0020
	}
	return r
}

// saslPrep normalizes the password per ISO 32000-2 §7.6.4.3.3 (SASLprep,
// RFC 4013) and truncates the UTF-8 result to 127 bytes. Implemented subset:
// RFC 3454 table B.1 code points are removed, table C.1.2 non-ASCII spaces
// map to U+0020 (B.1 wins on the U+200B overlap, matching libidn), and the
// result is NFKC-normalized. The prohibited-output and bidi checks are
// intentionally omitted: a password Acrobat would have rejected at
// encryption time cannot exist in a real file, and leniency here can only
// make authentication fail, which is the status quo for such input.
func saslPrep(password string) []byte {
	mapped := strings.Map(saslPrepMap, password)
	normalized := norm.NFKC.String(mapped)
	b := []byte(normalized)
	if len(b) > 127 {
		b = b[:127]
	}
	return b
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
