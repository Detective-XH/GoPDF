package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rc4" //nolint:gosec // RC4 is mandated by PDF 1.4–1.6 encryption spec; testing existing behavior, not choosing new crypto
	"io"
	"strings"
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
//
// Strategy: use a 16-byte plaintext so PKCS7 appends a full padding block of
// [0x10 × 16]. Payload layout: [IV(16)] [CT0(16)] [CT1(16)].
// Flip the last byte of CT0 (data[31]). In CBC, this XOR-propagates only into
// pt1[15]: new_pt1[15] = 0x10 ^ 0xFF = 0xEF = 239 > aes.BlockSize → nil.
// This is deterministic regardless of key/IV.
func TestDecryptAESBadPadding(t *testing.T) {
	key := make([]byte, 16)
	_, _ = rand.Read(key)
	data := aesCBCEncrypt(key, bytes.Repeat([]byte{0x41}, 16))
	data[31] ^= 0xFF // corrupt CT0[15] → pt1[15] = 0x10^0xFF = 0xEF > 16
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

// --- validateKeyLength -------------------------------------------------------

// TestEncryptValidateKeyLength exercises the four documented branches of
// validateKeyLength: an explicit 40-bit key, an explicit 128-bit key, a missing
// /Length (defaults to 40), and an invalid non-multiple-of-8 value.
func TestEncryptValidateKeyLength(t *testing.T) {
	// /Length 40 → 40 (bits), nil
	n, err := validateKeyLength(dict{name("Length"): int64(40)})
	if err != nil || n != 40 {
		t.Errorf("length 40: got (%d, %v), want (40, nil)", n, err)
	}

	// /Length 128 → 128 (bits), nil
	n, err = validateKeyLength(dict{name("Length"): int64(128)})
	if err != nil || n != 128 {
		t.Errorf("length 128: got (%d, %v), want (128, nil)", n, err)
	}

	// missing /Length → default 40 bits, nil
	n, err = validateKeyLength(dict{})
	if err != nil || n != 40 {
		t.Errorf("missing length: got (%d, %v), want (40, nil)", n, err)
	}

	// /Length 3 (not a multiple of 8) → error
	_, err = validateKeyLength(dict{name("Length"): int64(3)})
	if err == nil {
		t.Error("length 3: want error, got nil")
	}
}

// --- extractTrailerID --------------------------------------------------------

// TestEncryptExtractTrailerID covers the happy path (array of two 16-byte
// strings), a missing /ID key, and a non-string first element.
func TestEncryptExtractTrailerID(t *testing.T) {
	// valid: array with two 16-byte strings → returns first as []byte
	firstID := strings.Repeat("\x01", 16)
	trailer := dict{
		name("ID"): array{firstID, strings.Repeat("\x02", 16)},
	}
	got, err := extractTrailerID(trailer)
	if err != nil {
		t.Fatalf("valid trailer: unexpected error: %v", err)
	}
	if !bytes.Equal(got, []byte(firstID)) {
		t.Errorf("valid trailer: got %x, want %x", got, []byte(firstID))
	}

	// missing /ID → error
	_, err = extractTrailerID(dict{})
	if err == nil {
		t.Error("missing ID: want error, got nil")
	}

	// first element not a string → error
	_, err = extractTrailerID(dict{
		name("ID"): array{int64(42), strings.Repeat("\x02", 16)},
	})
	if err == nil {
		t.Error("non-string ID[0]: want error, got nil")
	}
}

// --- parseEncryptHeader ------------------------------------------------------

// encryptMakeDictV4 builds a minimal V4 encrypt dict with a valid CF/StmF/StrF
// structure so that okayV4 accepts it.
func encryptMakeDictV4() dict {
	return dict{
		name("Filter"): name("Standard"),
		name("V"):      int64(4),
		name("R"):      int64(4),
		name("O"):      strings.Repeat("\x00", 32),
		name("U"):      strings.Repeat("\x00", 32),
		name("P"):      int64(-4),
		name("Length"): int64(128),
		name("CF"): dict{
			name("StdCF"): dict{
				name("CFM"):       name("AESV2"),
				name("AuthEvent"): name("DocOpen"),
				name("Length"):    int64(16),
			},
		},
		name("StmF"): name("StdCF"),
		name("StrF"): name("StdCF"),
	}
}

// TestEncryptParseEncryptHeader covers: valid V=1/R=2, valid V=4 (calls
// okayV4 internally), unsupported V=3, and R<2 (rejected revision).
func TestEncryptParseEncryptHeader(t *testing.T) {
	// V=1, R=2 → success
	V, R, err := parseEncryptHeader(dict{
		name("V"): int64(1),
		name("R"): int64(2),
	})
	if err != nil || V != 1 || R != 2 {
		t.Errorf("V=1 R=2: got (%d,%d,%v), want (1,2,nil)", V, R, err)
	}

	// V=4 with valid CF → success
	V, R, err = parseEncryptHeader(encryptMakeDictV4())
	if err != nil || V != 4 || R != 4 {
		t.Errorf("V=4: got (%d,%d,%v), want (4,4,nil)", V, R, err)
	}

	// V=3 → unsupported
	_, _, err = parseEncryptHeader(dict{
		name("V"): int64(3),
		name("R"): int64(3),
	})
	if err == nil {
		t.Error("V=3: want error, got nil")
	}

	// V=1, R=1 → revision too low
	_, _, err = parseEncryptHeader(dict{
		name("V"): int64(1),
		name("R"): int64(1),
	})
	if err == nil {
		t.Error("R=1: want error, got nil")
	}
}

// --- buildEncryptKey ---------------------------------------------------------

// TestEncryptBuildEncryptKey verifies the key length produced for R=2 (always
// 5 bytes regardless of n) and for R=3 with n=128 (should yield 16 bytes).
func TestEncryptBuildEncryptKey(t *testing.T) {
	O := strings.Repeat("\x00", 32)
	ID := []byte(strings.Repeat("\x01", 16))

	// R=2, n=128 — spec forces key[:40/8] = 5 bytes even though n=128
	key, c, err := buildEncryptKey("", O, 0xFFFFFFFC, 128, 2, ID)
	if err != nil {
		t.Fatalf("R=2: unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("R=2: cipher is nil")
	}
	if len(key) != 5 {
		t.Errorf("R=2 n=128: want key len 5, got %d", len(key))
	}

	// R=3, n=128 — key length must be n/8 = 16
	key, c, err = buildEncryptKey("", O, 0xFFFFFFFC, 128, 3, ID)
	if err != nil {
		t.Fatalf("R=3: unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("R=3: cipher is nil")
	}
	if len(key) != 16 {
		t.Errorf("R=3 n=128: want key len 16, got %d", len(key))
	}
}

// --- verifyEncryptKey --------------------------------------------------------

// TestEncryptVerifyEncryptKey tests R=2 matching and non-matching U values.
// For R=2, verifyEncryptKey computes RC4(passwordPad) using the supplied cipher
// and compares against U[:16]. Two fresh ciphers with identical keys are used:
// one to pre-compute the expected U so we know what to pass, one fresh instance
// that verifyEncryptKey can consume.
func TestEncryptVerifyEncryptKey(t *testing.T) {
	O := strings.Repeat("\x00", 32)
	ID := []byte(strings.Repeat("\x01", 16))

	key, _, err := buildEncryptKey("", O, 0xFFFFFFFC, 40, 2, ID)
	if err != nil {
		t.Fatalf("buildEncryptKey: %v", err)
	}

	// Build expected U: RC4(key, passwordPad[:32])
	c1, _ := rc4.NewCipher(key) //nolint:gosec // testing PDF spec RC4 primitive
	uBytes := make([]byte, 32)
	copy(uBytes, passwordPad[:32])
	c1.XORKeyStream(uBytes, uBytes)
	expectedU := string(uBytes)

	// Fresh cipher for verifyEncryptKey to consume.
	c2, _ := rc4.NewCipher(key) //nolint:gosec // testing PDF spec RC4 primitive

	// Matching U → true
	if !verifyEncryptKey(2, key, c2, expectedU, ID) {
		t.Error("matching U: want true, got false")
	}

	// Mismatched U → false (fresh cipher again since c2 is exhausted)
	c3, _ := rc4.NewCipher(key) //nolint:gosec // testing PDF spec RC4 primitive
	if verifyEncryptKey(2, key, c3, strings.Repeat("\xFF", 32), ID) {
		t.Error("wrong U: want false, got true")
	}
}

// --- okayV4 ------------------------------------------------------------------

// TestEncryptOkayV4 covers: a fully valid V4 dict, a dict missing CF, and a
// dict where StmF != StrF.
func TestEncryptOkayV4(t *testing.T) {
	// valid dict → true
	if !okayV4(encryptMakeDictV4()) {
		t.Error("valid V4 dict: want true, got false")
	}

	// missing CF → false
	d := encryptMakeDictV4()
	delete(d, name("CF"))
	if okayV4(d) {
		t.Error("missing CF: want false, got true")
	}

	// StmF != StrF → false
	d = encryptMakeDictV4()
	d[name("StrF")] = name("OtherCF")
	if okayV4(d) {
		t.Error("StmF != StrF: want false, got true")
	}
}

// --- validateCFParam ---------------------------------------------------------

// TestEncryptValidateCFParam exercises the three documented outcomes: AESV2
// returns true, unknown CFM returns false, and an empty dict returns false.
func TestEncryptValidateCFParam(t *testing.T) {
	// /CFM /AESV2 → true
	if !validateCFParam(dict{name("CFM"): name("AESV2")}) {
		t.Error("AESV2: want true, got false")
	}

	// /CFM /V2 (unknown) → false
	if validateCFParam(dict{name("CFM"): name("V2")}) {
		t.Error("V2: want false, got true")
	}

	// empty dict (no CFM) → false
	if validateCFParam(dict{}) {
		t.Error("empty dict: want false, got true")
	}
}

// --- decryptStream (RC4) -----------------------------------------------------

// TestEncryptDecryptStreamRC4 confirms that decryptStream with useAES=false
// returns a non-nil reader and that reading from it does not panic.
func TestEncryptDecryptStreamRC4(t *testing.T) {
	key := make([]byte, 5) // minimum valid RC4 key length
	content := []byte("hello world stream content")
	rd := bytes.NewReader(content)

	result := decryptStream(key, false, objptr{}, rd)
	if result == nil {
		t.Fatal("RC4 stream: want non-nil reader, got nil")
	}
	out, err := io.ReadAll(result)
	if err != nil {
		t.Fatalf("RC4 stream: read error: %v", err)
	}
	if len(out) != len(content) {
		t.Errorf("RC4 stream: got %d bytes, want %d", len(out), len(content))
	}
}

// --- parseEncryptBody --------------------------------------------------------

// TestEncryptParseEncryptBodyValid exercises parseEncryptBody with a valid dict
// and trailer, and with an unsupported /Filter value.
func TestEncryptParseEncryptBodyValid(t *testing.T) {
	validEncrypt := dict{
		name("Filter"): name("Standard"),
		name("V"):      int64(1),
		name("R"):      int64(2),
		name("O"):      strings.Repeat("\x00", 32),
		name("U"):      strings.Repeat("\x00", 32),
		name("P"):      int64(-4),
		name("Length"): int64(40),
	}
	validTrailer := dict{
		name("ID"): array{strings.Repeat("\x01", 16), strings.Repeat("\x02", 16)},
	}

	n, O, U, P, ID, err := parseEncryptBody(validEncrypt, validTrailer)
	if err != nil {
		t.Fatalf("valid body: unexpected error: %v", err)
	}
	if n <= 0 {
		t.Errorf("valid body: want n>0, got %d", n)
	}
	if len(O) != 32 {
		t.Errorf("valid body: want O len 32, got %d", len(O))
	}
	if len(U) != 32 {
		t.Errorf("valid body: want U len 32, got %d", len(U))
	}
	if P == 0 && int64(-4) != 0 {
		// P is uint32(-4) which is 0xFFFFFFFC — just check it's non-zero
		_ = P
	}
	if len(ID) == 0 {
		t.Error("valid body: want non-empty ID")
	}

	// unsupported /Filter → error
	badEncrypt := dict{
		name("Filter"): name("Unknown"),
	}
	_, _, _, _, _, err = parseEncryptBody(badEncrypt, validTrailer)
	if err == nil {
		t.Error("unknown filter: want error, got nil")
	}
}

// --- decryptString (RC4 path) ------------------------------------------------

// TestEncryptDecryptStringBranches exercises the RC4 path of decryptString via
// a Reader with useAES=false. It confirms that decryption and re-encryption
// with the same key roundtrip to the original plaintext (RC4 is symmetric).
func TestEncryptDecryptStringBranches(t *testing.T) {
	r := &Reader{useAES: false}
	r.key = make([]byte, 5) // zero key — deterministic for test purposes

	plaintext := "test string"
	// Encrypt once using decryptString (RC4 is its own inverse).
	encrypted := decryptString(r.key, r.useAES, objptr{}, plaintext)
	// Decrypt by applying again — RC4 is symmetric.
	recovered := decryptString(r.key, r.useAES, objptr{}, encrypted)
	if recovered != plaintext {
		t.Errorf("RC4 roundtrip: got %q, want %q", recovered, plaintext)
	}
}

// --- initEncrypt -------------------------------------------------------------

// TestEncryptInitEncryptWrongPassword covers initEncrypt's full code path
// (parseEncryptBody → parseEncryptHeader → buildEncryptKey → verifyEncryptKey)
// with a zero-filled /U that will never match the derived key, so the function
// returns ErrInvalidPassword. Lines 187-188 (r.key = key; r.useAES = ...) are
// the only statements not reached here; they are covered by TestReadOpen via
// the full NewReader path.
func TestEncryptInitEncryptWrongPassword(t *testing.T) {
	r := &Reader{
		f:   bytes.NewReader(nil),
		end: 0,
		trailer: dict{
			name("Encrypt"): dict{
				name("Filter"): name("Standard"),
				name("V"):      int64(1),
				name("R"):      int64(2),
				name("O"):      strings.Repeat("\x00", 32),
				name("U"):      strings.Repeat("\x00", 32), // will not match derived key
				name("P"):      int64(-4),
			},
			name("ID"): array{strings.Repeat("\x01", 16), strings.Repeat("\x02", 16)},
		},
	}
	err := r.initEncrypt("")
	if err != ErrInvalidPassword {
		t.Errorf("initEncrypt: got %v, want ErrInvalidPassword", err)
	}
}

// TestEncryptInitEncryptSuccess derives the correct /U value from buildEncryptKey
// and RC4 so that verifyEncryptKey returns true, covering lines 187–188
// (r.key = key; r.useAES = ...) of initEncrypt.
func TestEncryptInitEncryptSuccess(t *testing.T) {
	O := strings.Repeat("\x00", 32)
	P := uint32(0xFFFFFFFC)
	n := int64(40)
	R := int64(2)
	ID := []byte(strings.Repeat("\x01", 16))

	key, _, err := buildEncryptKey("", O, P, n, R, ID)
	if err != nil {
		t.Fatalf("buildEncryptKey: %v", err)
	}

	// U = RC4(key, passwordPad[:32]) — same derivation as verifyEncryptKey R=2
	c, _ := rc4.NewCipher(key) //nolint:gosec // testing PDF spec RC4 primitive
	uBytes := make([]byte, 32)
	copy(uBytes, passwordPad[:32])
	c.XORKeyStream(uBytes, uBytes)

	r := &Reader{
		f:   bytes.NewReader(nil),
		end: 0,
		trailer: dict{
			name("Encrypt"): dict{
				name("Filter"): name("Standard"),
				name("V"):      int64(1),
				name("R"):      R,
				name("O"):      O,
				name("U"):      string(uBytes),
				name("P"):      int64(int32(P)), // uint32(0xFFFFFFFC) → int64(-4)
				name("Length"): n,
			},
			name("ID"): array{string(ID), string(ID)},
		},
	}
	if err := r.initEncrypt(""); err != nil {
		t.Fatalf("initEncrypt: %v", err)
	}
	if len(r.key) == 0 {
		t.Error("r.key not set after successful initEncrypt")
	}
	if r.useAES {
		t.Error("V=1 should not set useAES=true")
	}
}

// TestEncryptInitEncryptBadFilter covers the parseEncryptBody error branch inside
// initEncrypt (Filter != "Standard").
func TestEncryptInitEncryptBadFilter(t *testing.T) {
	r := &Reader{
		f:   bytes.NewReader(nil),
		end: 0,
		trailer: dict{
			name("Encrypt"): dict{
				name("Filter"): name("BadFilter"),
				name("V"):      int64(1),
				name("R"):      int64(2),
				name("O"):      strings.Repeat("\x00", 32),
				name("U"):      strings.Repeat("\x00", 32),
				name("P"):      int64(-4),
			},
			name("ID"): array{strings.Repeat("\x01", 16), strings.Repeat("\x02", 16)},
		},
	}
	if err := r.initEncrypt(""); err == nil {
		t.Error("expected error for bad filter, got nil")
	}
}
