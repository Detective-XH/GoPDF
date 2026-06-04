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
)

// initEncryptAES256 handles V=5 (R=5 / R=6). On success it sets r.key (the
// 32-byte file encryption key), r.useAES, and r.aes256.
func (r *Reader) initEncryptAES256(encrypt dict, password string) error {
	if encrypt["Filter"] != name("Standard") {
		return fmt.Errorf("unsupported PDF: encryption filter %v", objfmt(encrypt["Filter"]))
	}
	rev, _ := encrypt["R"].(int64)
	if rev != 5 && rev != 6 {
		return fmt.Errorf("unsupported PDF: AES-256 revision R=%d", rev)
	}
	if !okayV5(encrypt) {
		return fmt.Errorf("unsupported PDF: V=5 crypt filter %v", objfmt(encrypt))
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
	r.useAES = true
	r.aes256 = true
	return nil
}

// okayV5 accepts only the AESV3/DocOpen/256-bit crypt-filter configuration.
func okayV5(encrypt dict) bool {
	cf, ok := encrypt["CF"].(dict)
	if !ok {
		return false
	}
	stmf, ok := encrypt["StmF"].(name)
	if !ok {
		return false
	}
	strf, ok := encrypt["StrF"].(name)
	if !ok || stmf != strf {
		return false
	}
	cfparam, _ := cf[stmf].(dict)
	return validateCFParamV5(cfparam)
}

// validateCFParamV5 checks the StdCF dict is AESV3 over DocOpen.
func validateCFParamV5(cfparam dict) bool {
	if cfparam["AuthEvent"] != nil && cfparam["AuthEvent"] != name("DocOpen") {
		return false
	}
	if cfparam["Length"] != nil && cfparam["Length"] != int64(32) && cfparam["Length"] != int64(256) {
		return false
	}
	return cfparam["CFM"] == name("AESV3")
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

// saslPrep truncates the UTF-8 password to 127 bytes per §7.6.4.3.3. Full
// SASLprep (RFC 4013) normalisation is a documented limitation; ASCII and empty
// passwords — the overwhelming majority — are unaffected.
func saslPrep(password string) []byte {
	b := []byte(password)
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
