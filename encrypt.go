package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rc4"
	"crypto/subtle"
	"fmt"
	"io"
)

var passwordPad = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80, 0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

// cipherMode selects the decryption algorithm for one crypt-filter class
// (streams or strings). The zero value means "document not encrypted".
type cipherMode int

const (
	modeNone     cipherMode = iota // unencrypted document (zero value)
	modeRC4                        // V=1/2, or V=4 CFM /V2
	modeAESV2                      // V=4 CFM /AESV2 (AES-128-CBC)
	modeAESV3                      // V=5 CFM /AESV3 (AES-256-CBC, no per-object key)
	modeIdentity                   // CFM /Identity or filter name /Identity: pass-through
)

// tryDecrypt handles the optional encryption step.  It tries the empty
// password first, then each password returned by pw in turn.  It returns nil
// when the file is unencrypted or decryption succeeds.
func tryDecrypt(r *Reader, pw func() string) error {
	if r.trailer["Encrypt"] == nil {
		return nil
	}
	err := r.initEncrypt("")
	if err == nil {
		return nil
	}
	if pw == nil || err != ErrInvalidPassword {
		return err
	}
	for {
		next := pw()
		if next == "" {
			break
		}
		if r.initEncrypt(next) == nil {
			return nil
		}
	}
	return err
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
	// Fix 2: constant-time comparison to avoid timing side-channel.
	return subtle.ConstantTimeCompare([]byte(U)[:len(u)], u) == 1
}

func (r *Reader) initEncrypt(password string) error {
	encrypt, _ := r.resolve(objptr{}, r.trailer["Encrypt"]).data.(dict)
	if V, _ := encrypt["V"].(int64); V == 5 {
		return r.initEncryptAES256(encrypt, password)
	}
	n, O, U, P, ID, err := parseEncryptBody(encrypt, r.trailer)
	if err != nil {
		return err
	}
	V, R, err := parseEncryptHeader(encrypt)
	if err != nil {
		return err
	}
	stm, str, err := resolveCryptFilters(encrypt, V)
	if err != nil {
		return err
	}
	em := encryptMetadataFlag(encrypt)
	key, c, err := buildEncryptKey(password, O, P, n, R, ID, em)
	if err != nil {
		return err
	}
	if !verifyEncryptKey(R, key, c, U, ID) {
		// The password failed as the user password — try it as the owner
		// password (PDF 32000-1:2008 §7.6.3.4 Algorithm 7).
		if key, err = ownerAuthKey(password, O, U, P, n, R, ID, em); err != nil {
			return err
		}
	}
	r.key = key
	r.stmMode, r.strMode = stm, str
	r.encryptMetadata = em
	return nil
}

// ownerEncryptKey derives the RC4 key that encrypts the /O entry from the
// owner password (PDF 32000-1:2008 §7.6.3.4 Algorithm 3 steps a–d). The 50
// MD5 iterations hash only the first n/8 digest bytes, matching the
// Algorithm 2 loop in buildEncryptKey: Adobe's wording for step (c) omits
// the truncation, but producers (Ghostscript, qpdf) apply it, and the
// difference is observable only when n < 128 — verified against the
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
// given the owner password (PDF 32000-1:2008 §7.6.3.4 Algorithm 7 steps a–b).
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

var ErrInvalidPassword = fmt.Errorf("encrypted PDF: invalid password")

// resolveCryptFilters maps the encrypt dict's StmF/StrF entries to cipher
// modes (ISO 32000-1 §7.6.5). V=1/2 use no crypt filters: both classes are
// RC4. For V=4/V=5 each class resolves independently: the built-in /Identity
// filter is a pass-through; named filters must appear in /CF with a CFM this
// library supports (V2, AESV2 for V=4; AESV3 for V=5) over AuthEvent DocOpen.
func resolveCryptFilters(encrypt dict, V int64) (stm, str cipherMode, err error) {
	if V == 1 || V == 2 {
		return modeRC4, modeRC4, nil
	}
	cf, _ := encrypt["CF"].(dict)
	stm, err = resolveOneCryptFilter(cf, encrypt["StmF"], V)
	if err != nil {
		return 0, 0, fmt.Errorf("unsupported PDF: StmF: %v", err)
	}
	str, err = resolveOneCryptFilter(cf, encrypt["StrF"], V)
	if err != nil {
		return 0, 0, fmt.Errorf("unsupported PDF: StrF: %v", err)
	}
	return stm, str, nil
}

// resolveOneCryptFilter resolves a single StmF/StrF entry. Per §7.6.5, an
// absent entry defaults to /Identity; a present entry of the wrong type is
// malformed and must fail closed — treating it as /Identity would let
// encrypted data pass through undecrypted.
func resolveOneCryptFilter(cf dict, entry any, V int64) (cipherMode, error) {
	if entry == nil {
		return modeIdentity, nil
	}
	fname, ok := entry.(name)
	if !ok {
		return 0, fmt.Errorf("malformed crypt filter name %v", objfmt(entry))
	}
	if fname == "Identity" {
		return modeIdentity, nil
	}
	cfparam, _ := cf[fname].(dict)
	if cfparam == nil {
		return 0, fmt.Errorf("crypt filter %v not in /CF", fname)
	}
	if cfparam["AuthEvent"] != nil && cfparam["AuthEvent"] != name("DocOpen") {
		return 0, fmt.Errorf("crypt filter %v: unsupported AuthEvent", fname)
	}
	return cfmMode(cfparam, V)
}

// cfmMode maps a /CFM name to a cipherMode, validating /Length per method.
func cfmMode(cfparam dict, V int64) (cipherMode, error) {
	cfm, _ := cfparam["CFM"].(name)
	length := cfparam["Length"]
	switch {
	case V == 4 && cfm == "AESV2":
		if length != nil && length != int64(16) && length != int64(128) {
			return 0, fmt.Errorf("AESV2 length %v", objfmt(length))
		}
		return modeAESV2, nil
	case V == 4 && cfm == "V2":
		return modeRC4, nil
	case V == 5 && cfm == "AESV3":
		if length != nil && length != int64(32) && length != int64(256) {
			return 0, fmt.Errorf("AESV3 length %v", objfmt(length))
		}
		return modeAESV3, nil
	case cfm == "Identity":
		return modeIdentity, nil
	}
	return 0, fmt.Errorf("unsupported CFM %v", objfmt(cfparam["CFM"]))
}

// encryptMetadataFlag reads /EncryptMetadata (default true, §7.6.3.2).
func encryptMetadataFlag(encrypt dict) bool {
	em, ok := encrypt["EncryptMetadata"].(bool)
	return !ok || em
}

// cryptKey derives the per-object key (PDF 32000-1:2008 §7.6.2 Algorithm 1).
// AESV3 uses the file key directly — no per-object derivation; Identity never
// reaches here (decryptString/decryptStream switch on mode first).
// Fix 1: truncate per-object key to min(len(key)+5, 16) per Algorithm 1 step (f).
func cryptKey(key []byte, mode cipherMode, ptr objptr) []byte {
	if mode == modeAESV3 {
		return key
	}
	h := md5.New()
	h.Write(key)
	h.Write([]byte{byte(ptr.id), byte(ptr.id >> 8), byte(ptr.id >> 16), byte(ptr.gen), byte(ptr.gen >> 8)})
	if mode == modeAESV2 {
		h.Write([]byte("sAlT"))
	}
	sum := h.Sum(nil)
	return sum[:min(len(key)+5, 16)]
}

// decryptAES decrypts an AES-CBC payload: data = [BlockSize IV] || [PKCS7-padded ciphertext].
// Modifies data in-place. Returns nil on any validation or cipher error.
func decryptAES(key, data []byte) []byte {
	if len(data) < aes.BlockSize || (len(data)-aes.BlockSize)%aes.BlockSize != 0 {
		return nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil
	}
	iv := data[:aes.BlockSize]
	ct := data[aes.BlockSize:]
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(ct, ct)
	if len(ct) == 0 {
		return nil
	}
	pad := int(ct[len(ct)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(ct) {
		return nil
	}
	// Verify every PKCS#7 padding byte equals pad (constant-time), rejecting
	// in-range but inconsistent padding such as 0x99 0x02.
	var diff byte
	for _, b := range ct[len(ct)-pad:] {
		diff |= b ^ byte(pad)
	}
	if diff != 0 {
		return nil
	}
	return ct[:len(ct)-pad]
}

func decryptString(key []byte, mode cipherMode, ptr objptr, x string) string {
	switch mode {
	case modeNone, modeIdentity:
		return x
	case modeAESV2, modeAESV3:
		if plain := decryptAES(cryptKey(key, mode, ptr), []byte(x)); plain != nil {
			return string(plain)
		}
		return ""
	}
	c, _ := rc4.NewCipher(cryptKey(key, mode, ptr))
	data := []byte(x)
	c.XORKeyStream(data, data)
	return string(data)
}

// Fix 4: read-all approach for AES decryption — handles padding and eliminates
// silent error fallthrough. The cbcReader struct is removed entirely.
func decryptStream(key []byte, mode cipherMode, ptr objptr, rd io.Reader) io.Reader {
	switch mode {
	case modeNone, modeIdentity:
		return rd
	case modeAESV2, modeAESV3:
		// Bound the in-memory read so a malformed huge stream cannot exhaust
		// memory. Read one byte past the cap to tell a fully-read stream from one
		// that overflows it, and surface the overflow instead of silently
		// truncating to corrupt/empty plaintext.
		data, err := io.ReadAll(io.LimitReader(rd, maxDecompressedSize+1))
		if err != nil {
			return bytes.NewReader(nil)
		}
		if int64(len(data)) > maxDecompressedSize {
			return &errorReadCloser{fmt.Errorf("encrypted stream exceeds %d-byte limit", maxDecompressedSize)}
		}
		if plain := decryptAES(cryptKey(key, mode, ptr), data); plain != nil {
			return bytes.NewReader(plain)
		}
		return bytes.NewReader(nil)
	}
	c, _ := rc4.NewCipher(cryptKey(key, mode, ptr))
	return &cipher.StreamReader{S: c, R: rd}
}
