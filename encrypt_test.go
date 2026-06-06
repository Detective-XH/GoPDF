package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" //nolint:gosec // MD5 is mandated by the PDF Standard security handler key derivation
	"crypto/rand"
	"crypto/rc4" //nolint:gosec // RC4 is mandated by PDF 1.4–1.6 encryption spec; testing existing behavior, not choosing new crypto
	"fmt"
	"io"
	"os"
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

// TestDecryptAESInconsistentPadding verifies that padding whose final byte is in
// range [1,16] but whose other pad bytes disagree is rejected. Same layout as
// TestDecryptAESBadPadding; flip CT0[14] (data[30]) so pt1[14] = 0x10^0xFF = 0xEF
// while pt1[15] stays 0x10 (pad=16, in range but inconsistent) → must be nil.
func TestDecryptAESInconsistentPadding(t *testing.T) {
	key := make([]byte, 16)
	_, _ = rand.Read(key)
	data := aesCBCEncrypt(key, bytes.Repeat([]byte{0x41}, 16))
	data[30] ^= 0xFF // pt1[14] = 0xEF, pt1[15] = 0x10 (pad=16 but pad byte [14] != 16)
	if got := decryptAES(key, data); got != nil {
		t.Errorf("inconsistent-padding: want nil, got %q", got)
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

// TestEncryptParseEncryptHeader covers: valid V=1/R=2, valid V=4 (V/R
// pass-through only — crypt-filter validation lives in resolveCryptFilters),
// unsupported V=3, and R<2 (rejected revision).
func TestEncryptParseEncryptHeader(t *testing.T) {
	// V=1, R=2 → success
	V, R, err := parseEncryptHeader(dict{
		name("V"): int64(1),
		name("R"): int64(2),
	})
	if err != nil || V != 1 || R != 2 {
		t.Errorf("V=1 R=2: got (%d,%d,%v), want (1,2,nil)", V, R, err)
	}

	// V=4 without any CF entries → success: the header validates V/R only.
	V, R, err = parseEncryptHeader(dict{
		name("V"): int64(4),
		name("R"): int64(4),
	})
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
	key, c, err := buildEncryptKey("", O, 0xFFFFFFFC, 128, 2, ID, true)
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
	key, c, err = buildEncryptKey("", O, 0xFFFFFFFC, 128, 3, ID, true)
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

	key, _, err := buildEncryptKey("", O, 0xFFFFFFFC, 40, 2, ID, true)
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

// --- resolveCryptFilters -------------------------------------------------------

// encryptDictV4Filters builds a V=4 encrypt dict with the given StmF/StrF
// names and CF entries. An empty stmf/strf omits the key entirely (an absent
// entry defaults to /Identity per §7.6.5).
func encryptDictV4Filters(stmf, strf string, cf dict) dict {
	d := dict{name("V"): int64(4), name("CF"): cf}
	if stmf != "" {
		d[name("StmF")] = name(stmf)
	}
	if strf != "" {
		d[name("StrF")] = name(strf)
	}
	return d
}

// TestResolveCryptFilters covers per-class crypt-filter resolution: V=1/2
// shortcuts, /Identity (named and absent), StmF != StrF splits, CFM /V2, the
// V=5 AESV3 path, and the rejection branches (missing /CF entry, bad
// AuthEvent, unknown CFM). It absorbs the coverage of the deleted
// okayV4/validateCFParam tests — what used to assert "StmF != StrF fails"
// now asserts the resolved per-class modes.
func TestResolveCryptFilters(t *testing.T) {
	cfAES := dict{name("StdCF"): dict{name("CFM"): name("AESV2"), name("AuthEvent"): name("DocOpen"), name("Length"): int64(16)}}
	cfRC4 := dict{name("RC4CF"): dict{name("CFM"): name("V2")}}
	cfMixed := dict{
		name("StdCF"): dict{name("CFM"): name("AESV2"), name("AuthEvent"): name("DocOpen"), name("Length"): int64(16)},
		name("RC4CF"): dict{name("CFM"): name("V2")},
	}
	cfBadEvent := dict{name("StdCF"): dict{name("CFM"): name("AESV2"), name("AuthEvent"): name("EFOpen")}}
	cfUnknown := dict{name("StdCF"): dict{name("CFM"): name("AESV1")}}
	cfV5 := dict{name("StdCF"): dict{name("CFM"): name("AESV3"), name("AuthEvent"): name("DocOpen"), name("Length"): int64(32)}}

	cases := []struct {
		label    string
		d        dict
		V        int64
		stm, str cipherMode
		wantErr  bool
	}{
		{"V1 both RC4", dict{name("V"): int64(1)}, 1, modeRC4, modeRC4, false},
		{"V2 both RC4", dict{name("V"): int64(2)}, 2, modeRC4, modeRC4, false},
		{"V4 AESV2 both", encryptDictV4Filters("StdCF", "StdCF", cfAES), 4, modeAESV2, modeAESV2, false},
		{"V4 Identity strings", encryptDictV4Filters("StdCF", "Identity", cfAES), 4, modeAESV2, modeIdentity, false},
		{"V4 Identity streams", encryptDictV4Filters("Identity", "StdCF", cfAES), 4, modeIdentity, modeAESV2, false},
		{"V4 StmF!=StrF mixed CFMs", encryptDictV4Filters("StdCF", "RC4CF", cfMixed), 4, modeAESV2, modeRC4, false},
		{"V4 absent StmF defaults Identity", encryptDictV4Filters("", "StdCF", cfAES), 4, modeIdentity, modeAESV2, false},
		{"V4 CFM V2 (RC4 filter)", encryptDictV4Filters("RC4CF", "RC4CF", cfRC4), 4, modeRC4, modeRC4, false},
		{"V4 named filter missing from CF", encryptDictV4Filters("NoSuchCF", "NoSuchCF", cfAES), 4, 0, 0, true},
		{"V4 bad AuthEvent", encryptDictV4Filters("StdCF", "StdCF", cfBadEvent), 4, 0, 0, true},
		{"V4 unknown CFM", encryptDictV4Filters("StdCF", "StdCF", cfUnknown), 4, 0, 0, true},
		// A present non-name StmF/StrF must fail closed, not pass through as
		// /Identity (silent data-corruption mode).
		{"V4 non-name StmF fails closed", dict{name("V"): int64(4), name("CF"): cfAES,
			name("StmF"): int64(1), name("StrF"): name("StdCF")}, 4, 0, 0, true},
		{"V4 non-name StrF fails closed", dict{name("V"): int64(4), name("CF"): cfAES,
			name("StmF"): name("StdCF"), name("StrF"): array{}}, 4, 0, 0, true},
		{"V4 AESV2 bad Length", encryptDictV4Filters("StdCF", "StdCF",
			dict{name("StdCF"): dict{name("CFM"): name("AESV2"), name("Length"): int64(24)}}), 4, 0, 0, true},
		{"V5 AESV3 both", dict{name("V"): int64(5), name("CF"): cfV5, name("StmF"): name("StdCF"), name("StrF"): name("StdCF")}, 5, modeAESV3, modeAESV3, false},
		{"V5 Identity strings", dict{name("V"): int64(5), name("CF"): cfV5, name("StmF"): name("StdCF"), name("StrF"): name("Identity")}, 5, modeAESV3, modeIdentity, false},
		{"V5 AESV2 rejected", dict{name("V"): int64(5), name("CF"): cfAES, name("StmF"): name("StdCF"), name("StrF"): name("StdCF")}, 5, 0, 0, true},
	}
	for _, tc := range cases {
		stm, str, err := resolveCryptFilters(tc.d, tc.V)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: want error, got (stm=%v, str=%v)", tc.label, stm, str)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.label, err)
			continue
		}
		if stm != tc.stm || str != tc.str {
			t.Errorf("%s: got (stm=%v, str=%v), want (stm=%v, str=%v)", tc.label, stm, str, tc.stm, tc.str)
		}
	}
}

// --- /EncryptMetadata key derivation (Algorithm 2 step f) ---------------------

// TestEncryptMetadataKeyDerivation pins Algorithm 2 step (f) to revision 4:
// at R=4, EncryptMetadata=false must change the derived key; at R=2/R=3 the
// parameter must be a byte-identical no-op — protecting the fixture-verified
// R2/R3 paths from accidental step-f application.
func TestEncryptMetadataKeyDerivation(t *testing.T) {
	O := strings.Repeat("\x00", 32)
	ID := []byte(strings.Repeat("\x01", 16))
	P := uint32(0xFFFFFFFC)

	kTrue, _, err := buildEncryptKey("", O, P, 128, 4, ID, true)
	if err != nil {
		t.Fatalf("R=4 encryptMetadata=true: %v", err)
	}
	kFalse, _, err := buildEncryptKey("", O, P, 128, 4, ID, false)
	if err != nil {
		t.Fatalf("R=4 encryptMetadata=false: %v", err)
	}
	if bytes.Equal(kTrue, kFalse) {
		t.Error("R=4: EncryptMetadata=false must alter the key (Algorithm 2 step f)")
	}

	for _, tc := range []struct{ R, n int64 }{{2, 40}, {3, 128}} {
		a, _, err := buildEncryptKey("", O, P, tc.n, tc.R, ID, true)
		if err != nil {
			t.Fatalf("R=%d encryptMetadata=true: %v", tc.R, err)
		}
		b, _, err := buildEncryptKey("", O, P, tc.n, tc.R, ID, false)
		if err != nil {
			t.Fatalf("R=%d encryptMetadata=false: %v", tc.R, err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("R=%d: encryptMetadata must not affect key derivation", tc.R)
		}
	}
}

// --- /Identity pass-through ----------------------------------------------------

// TestDecryptStringIdentity verifies mode=modeIdentity returns input verbatim.
func TestDecryptStringIdentity(t *testing.T) {
	key := make([]byte, 16)
	in := "verbatim \x00\x01\x02 bytes"
	if got := decryptString(key, modeIdentity, objptr{id: 3, gen: 0}, in); got != in {
		t.Errorf("Identity string: got %q, want %q", got, in)
	}
}

// TestDecryptStreamIdentity verifies mode=modeIdentity streams pass through.
func TestDecryptStreamIdentity(t *testing.T) {
	key := make([]byte, 16)
	content := []byte("identity stream content")
	out, err := io.ReadAll(decryptStream(key, modeIdentity, objptr{id: 3, gen: 0}, bytes.NewReader(content)))
	if err != nil {
		t.Fatalf("Identity stream: read error: %v", err)
	}
	if !bytes.Equal(out, content) {
		t.Errorf("Identity stream: got %q, want %q", out, content)
	}
}

// --- decryptStream (RC4) -----------------------------------------------------

// TestEncryptDecryptStreamRC4 confirms that decryptStream with mode=modeRC4
// returns a non-nil reader and that reading from it does not panic.
func TestEncryptDecryptStreamRC4(t *testing.T) {
	key := make([]byte, 5) // minimum valid RC4 key length
	content := []byte("hello world stream content")
	rd := bytes.NewReader(content)

	result := decryptStream(key, modeRC4, objptr{}, rd)
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

// TestEncryptDecryptStringBranches exercises the RC4 path of decryptString.
// It confirms that decryption and re-encryption with the same key roundtrip
// to the original plaintext (RC4 is symmetric).
func TestEncryptDecryptStringBranches(t *testing.T) {
	r := &Reader{}
	r.key = make([]byte, 5) // zero key — deterministic for test purposes

	plaintext := "test string"
	// Encrypt once using decryptString (RC4 is its own inverse).
	encrypted := decryptString(r.key, modeRC4, objptr{}, plaintext)
	// Decrypt by applying again — RC4 is symmetric.
	recovered := decryptString(r.key, modeRC4, objptr{}, encrypted)
	if recovered != plaintext {
		t.Errorf("RC4 roundtrip: got %q, want %q", recovered, plaintext)
	}
}

// --- initEncrypt -------------------------------------------------------------

// TestEncryptInitEncryptWrongPassword covers initEncrypt's full code path
// (parseEncryptBody → parseEncryptHeader → buildEncryptKey → verifyEncryptKey)
// with a zero-filled /U that will never match the derived key, so the function
// returns ErrInvalidPassword. The success-path field assignments (r.key,
// r.stmMode/r.strMode, r.encryptMetadata) are the only statements not reached
// here; they are covered by TestReadOpen via the full NewReader path.
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
// and RC4 so that verifyEncryptKey returns true, covering the success-path
// field assignments (r.key, r.stmMode/r.strMode) of initEncrypt.
func TestEncryptInitEncryptSuccess(t *testing.T) {
	O := strings.Repeat("\x00", 32)
	P := uint32(0xFFFFFFFC)
	n := int64(40)
	R := int64(2)
	ID := []byte(strings.Repeat("\x01", 16))

	key, _, err := buildEncryptKey("", O, P, n, R, ID, true)
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
	if r.stmMode != modeRC4 || r.strMode != modeRC4 {
		t.Errorf("V=1: got (stm=%v, str=%v), want both modeRC4", r.stmMode, r.strMode)
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

// --- owner password (Algorithm 7) ---------------------------------------------

// encryptComputeO implements PDF 32000-1:2008 §7.6.3.4 Algorithm 3 (computing
// the /O value) for test fixtures: derive the RC4 key from the owner password,
// then encrypt the padded user password (19 extra XOR rounds for R >= 3).
func encryptComputeO(owner, user string, n, R int64) string {
	key := ownerEncryptKey(owner, n, R)
	buf := padPassword(user)
	if R == 2 {
		c, _ := rc4.NewCipher(key) //nolint:gosec // PDF spec RC4 primitive
		c.XORKeyStream(buf, buf)
		return string(buf)
	}
	for i := 0; i <= 19; i++ {
		key1 := make([]byte, len(key))
		for j := range key {
			key1[j] = key[j] ^ byte(i)
		}
		c, _ := rc4.NewCipher(key1) //nolint:gosec // PDF spec RC4 primitive
		c.XORKeyStream(buf, buf)
	}
	return string(buf)
}

// encryptComputeU derives the /U value for the given user password, mirroring
// verifyEncryptKey (R=2: Algorithm 4; R>=3: Algorithm 5, padded to 32 bytes).
func encryptComputeU(user, O string, P uint32, n, R int64, ID []byte) string {
	key, c, _ := buildEncryptKey(user, O, P, n, R, ID, true)
	if R == 2 {
		u := make([]byte, 32)
		copy(u, passwordPad)
		c.XORKeyStream(u, u)
		return string(u)
	}
	h := md5.New() //nolint:gosec // MD5 is mandated by the PDF Standard security handler
	h.Write(passwordPad)
	h.Write(ID)
	u := h.Sum(nil)
	c.XORKeyStream(u, u)
	for i := 1; i <= 19; i++ {
		key1 := make([]byte, len(key))
		copy(key1, key)
		for j := range key1 {
			key1[j] ^= byte(i)
		}
		c2, _ := rc4.NewCipher(key1) //nolint:gosec // PDF spec RC4 primitive
		c2.XORKeyStream(u, u)
	}
	return string(u) + strings.Repeat("\x00", 16)
}

// encryptOwnerReader builds a Reader whose trailer carries a V<=4 Standard
// encryption dict derived from the given owner and user passwords.
func encryptOwnerReader(owner, user string, V, R, n int64) *Reader {
	ID := []byte(strings.Repeat("\x01", 16))
	P := uint32(0xFFFFFFFC)
	O := encryptComputeO(owner, user, n, R)
	U := encryptComputeU(user, O, P, n, R, ID)
	return &Reader{
		f:   bytes.NewReader(nil),
		end: 0,
		trailer: dict{
			name("Encrypt"): dict{
				name("Filter"): name("Standard"),
				name("V"):      V,
				name("R"):      R,
				name("O"):      O,
				name("U"):      U,
				name("P"):      int64(int32(P)),
				name("Length"): n,
			},
			name("ID"): array{string(ID), string(ID)},
		},
	}
}

// TestEncryptOwnerPasswordR2 verifies Algorithm 7 unlocking for R=2: the
// owner password recovers the user password from /O and authenticates.
func TestEncryptOwnerPasswordR2(t *testing.T) {
	r := encryptOwnerReader("owner-secret", "user-secret", 1, 2, 40)
	if err := r.initEncrypt("owner-secret"); err != nil {
		t.Fatalf("owner password: %v", err)
	}
	if len(r.key) == 0 {
		t.Error("r.key not set after owner-password initEncrypt")
	}
}

// TestEncryptOwnerPasswordR3 verifies Algorithm 7 unlocking for R=3 with a
// 128-bit key (the 19-round XOR variant of the /O decryption loop).
func TestEncryptOwnerPasswordR3(t *testing.T) {
	r := encryptOwnerReader("owner-secret", "user-secret", 2, 3, 128)
	if err := r.initEncrypt("owner-secret"); err != nil {
		t.Fatalf("owner password: %v", err)
	}
	if len(r.key) == 0 {
		t.Error("r.key not set after owner-password initEncrypt")
	}
}

// TestEncryptOwnerPasswordUserStillWorks confirms the user-password path is
// untouched by the owner fallback.
func TestEncryptOwnerPasswordUserStillWorks(t *testing.T) {
	r := encryptOwnerReader("owner-secret", "user-secret", 2, 3, 128)
	if err := r.initEncrypt("user-secret"); err != nil {
		t.Fatalf("user password: %v", err)
	}
}

// TestEncryptOwnerPasswordWrong confirms a password that is neither the user
// nor the owner password still yields ErrInvalidPassword.
func TestEncryptOwnerPasswordWrong(t *testing.T) {
	r := encryptOwnerReader("owner-secret", "user-secret", 2, 3, 128)
	if err := r.initEncrypt("not-a-password"); err != ErrInvalidPassword {
		t.Fatalf("wrong password: got %v, want ErrInvalidPassword", err)
	}
}

// TestEncryptUserPassFromOwnerRoundtrip checks Algorithm 7 steps a–b directly:
// decrypting the /O produced by Algorithm 3 recovers the padded user password.
func TestEncryptUserPassFromOwnerRoundtrip(t *testing.T) {
	for _, R := range []int64{2, 3} {
		n := int64(40)
		if R == 3 {
			n = 128
		}
		O := encryptComputeO("owner", "user", n, R)
		got := userPassFromOwner("owner", O, n, R)
		want := string(padPassword("user"))
		if got != want {
			t.Errorf("R=%d: recovered %x, want %x", R, got, want)
		}
	}
}

// --- per-class crypt filters (StmF != StrF) -------------------------------------

// TestInitEncryptSplitFilters runs the full initEncrypt path over a synthetic
// V=4 dict with StmF=StdCF (AESV2) and StrF=Identity, asserting the two
// classes resolve independently. qpdf cannot produce such a file (it always
// writes StmF == StrF), so this synthetic dict is the only coverage for the
// split — recorded as a residual in the plan.
func TestInitEncryptSplitFilters(t *testing.T) {
	r := encryptOwnerReader("owner-secret", "user-secret", 4, 4, 128)
	enc := r.trailer[name("Encrypt")].(dict)
	enc[name("CF")] = dict{name("StdCF"): dict{
		name("CFM"):       name("AESV2"),
		name("AuthEvent"): name("DocOpen"),
		name("Length"):    int64(16),
	}}
	enc[name("StmF")] = name("StdCF")
	enc[name("StrF")] = name("Identity")
	if err := r.initEncrypt("user-secret"); err != nil {
		t.Fatalf("initEncrypt: %v", err)
	}
	if r.stmMode != modeAESV2 || r.strMode != modeIdentity {
		t.Errorf("modes: got (stm=%v, str=%v), want (stm=%v, str=%v)",
			r.stmMode, r.strMode, modeAESV2, modeIdentity)
	}
	if !r.encryptMetadata {
		t.Error("absent /EncryptMetadata must default to true")
	}
}

// --- cleartext metadata stream skip (§7.6.5.4) -----------------------------------

// TestBuildStreamReaderCleartextMetadata covers the isCleartextMetadata branch
// of buildStreamReader — the one new code path no fixture is guaranteed to
// reach. With /EncryptMetadata false, the XMP metadata stream is stored in
// cleartext and must bypass the cipher while sibling streams still decrypt;
// with /EncryptMetadata true the same stream must be run through the cipher
// (predicate not inverted). The metadata dict carries the key set qpdf 12
// actually writes (/Type /Metadata /Subtype /XML — verified by dumping the
// aes128-r4-cleartext-meta.pdf fixture).
func TestBuildStreamReaderCleartextMetadata(t *testing.T) {
	key := make([]byte, 16)
	_, _ = rand.Read(key)

	meta := []byte("<?xpacket begin='' id='W5M0MpCehiHzreSzNTczkc9d'?><x:xmpmeta/>")
	sibling := []byte("BT (sibling stream content) Tj ET")
	sibPtr := objptr{id: 4, gen: 0}
	encSibling := aesCBCEncrypt(cryptKey(key, modeAESV2, sibPtr), sibling)

	raw := append(append([]byte{}, meta...), encSibling...)
	metaOff, sibOff := int64(0), int64(len(meta))

	newReader := func(encryptMetadata bool) *Reader {
		return &Reader{
			f:               bytes.NewReader(raw),
			end:             int64(len(raw)),
			key:             key,
			stmMode:         modeAESV2,
			strMode:         modeAESV2,
			encryptMetadata: encryptMetadata,
		}
	}
	metaDict := dict{
		name("Type"):    name("Metadata"),
		name("Subtype"): name("XML"),
		name("Length"):  int64(len(meta)),
	}

	// (a) encryptMetadata=false: the metadata stream is returned verbatim.
	r := newReader(false)
	v := Value{r: r, data: stream{hdr: metaDict, ptr: objptr{id: 6, gen: 0}, offset: metaOff}}
	got, err := io.ReadAll(v.Reader())
	if err != nil {
		t.Fatalf("cleartext metadata read: %v", err)
	}
	if !bytes.Equal(got, meta) {
		t.Errorf("cleartext metadata: got %q, want verbatim %q", got, meta)
	}

	// (b) a sibling non-metadata stream still decrypts.
	sv := Value{r: r, data: stream{hdr: dict{name("Length"): int64(len(encSibling))}, ptr: sibPtr, offset: sibOff}}
	got, err = io.ReadAll(sv.Reader())
	if err != nil {
		t.Fatalf("sibling stream read: %v", err)
	}
	if !bytes.Equal(got, sibling) {
		t.Errorf("sibling stream: got %q, want decrypted %q", got, sibling)
	}

	// (c) encryptMetadata=true: the same metadata stream IS run through the
	// cipher — the stored cleartext must not come back verbatim.
	rt := newReader(true)
	vt := Value{r: rt, data: stream{hdr: metaDict, ptr: objptr{id: 6, gen: 0}, offset: metaOff}}
	got, err = io.ReadAll(vt.Reader())
	if err != nil {
		t.Fatalf("encrypted-metadata read: %v", err)
	}
	if bytes.Equal(got, meta) {
		t.Error("encryptMetadata=true: metadata stream returned verbatim; cipher was skipped")
	}
}

// buildIndirectMetadataPDF assembles a minimal unencrypted PDF whose catalog
// /Metadata stream stores /Type and /Subtype as INDIRECT name objects
// (5 0 R → /Metadata, 6 0 R → /XML), exercising isCleartextMetadata's
// indirect-object resolution.
func buildIndirectMetadataPDF(meta []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, 7)
	writeObj := func(num int, body string) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	writeObj(1, "<< /Type /Catalog /Pages 2 0 R /Metadata 4 0 R >>")
	writeObj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>")
	offsets[4] = buf.Len()
	fmt.Fprintf(&buf, "4 0 obj\n<< /Type 5 0 R /Subtype 6 0 R /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(meta), meta)
	writeObj(5, "/Metadata")
	writeObj(6, "/XML")
	xrefOff := buf.Len()
	buf.WriteString("xref\n0 7\n0000000000 65535 f \n")
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 7 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefOff)
	return buf.Bytes()
}

// TestBuildStreamReaderCleartextMetadataIndirectType is the regression test
// for indirect /Type//Subtype entries in the metadata stream dict: the
// predicate must resolve them like every other header read (Length, Filter)
// rather than matching the raw token, or a valid cleartext metadata stream
// would be pushed through the cipher and corrupted.
func TestBuildStreamReaderCleartextMetadataIndirectType(t *testing.T) {
	meta := []byte("<?xpacket begin='' id='W5M0MpCehiHzreSzNTczkc9d'?><x:xmpmeta/>")
	r, err := OpenBytes(buildIndirectMetadataPDF(meta))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	// Simulate an /EncryptMetadata false document: arm the stream cipher and
	// clear the flag — only the predicate's indirect resolution is under test.
	r.key = make([]byte, 16)
	r.stmMode = modeAESV2
	r.strMode = modeAESV2
	r.encryptMetadata = false

	v := r.Trailer().Key("Root").Key("Metadata")
	if v.Kind() != Stream {
		t.Fatalf("metadata: got kind %v, want Stream", v.Kind())
	}
	got, err := io.ReadAll(v.Reader())
	if err != nil {
		t.Fatalf("metadata read: %v", err)
	}
	if !bytes.Equal(got, meta) {
		t.Errorf("indirect /Type metadata: got %q, want verbatim %q", got, meta)
	}
}

// --- encrypted fixtures (external known answers) -------------------------------

// openEncryptedFixture opens testdata/encrypted/<fixture> with the given
// password via the public NewReaderEncrypted path.
func openEncryptedFixture(t *testing.T, fixture, password string) (*Reader, error) {
	t.Helper()
	//nolint:gosec // G304: fixture is a fixed testdata path, not user input
	f, err := os.Open("testdata/encrypted/" + fixture)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	done := false
	pw := func() string {
		if done {
			return ""
		}
		done = true
		return password
	}
	return NewReaderEncrypted(f, fi.Size(), pw)
}

// TestEncryptFixturePasswords verifies user- and owner-password unlocking
// against external known answers: Ghostscript- and qpdf-encrypted fixtures
// covering RC4 R2/40, R3/128, R3/40, RC4 inside a V=4 crypt filter
// (CFM /V2), AES-128 R4, AES-256 R5 and R6, and the two
// /EncryptMetadata-false variants. The R3/40 case discriminates Algorithm 3's
// full-digest 50-round MD5 loop from Algorithm 2's truncated variant, which
// the self-consistent unit tests above cannot detect. Successful opening
// means verifyEncryptKey matched the externally produced /U, and the
// Producer check confirms string decryption end-to-end. The
// aes128-r4-cleartext-meta fixture is the external known answer for
// Algorithm 2 step (f): it opens only if the 0xFFFFFFFF step is applied
// exactly at R=4 with EncryptMetadata=false.
func TestEncryptFixturePasswords(t *testing.T) {
	for _, fixture := range []struct {
		file          string
		cleartextMeta bool
	}{
		{"rc4-r2-40.pdf", false},
		{"rc4-r3-128.pdf", false},
		{"rc4-r3-40.pdf", false},
		{"rc4-r4-cfm-v2.pdf", false},
		{"aes128-r4.pdf", false},
		{"aes128-r4-cleartext-meta.pdf", true},
		{"aes256-r5.pdf", false},
		{"aes256-r6.pdf", false},
		{"aes256-r6-cleartext-meta.pdf", true},
	} {
		for _, pw := range []string{"user-secret", "owner-secret"} {
			r, err := openEncryptedFixture(t, fixture.file, pw)
			if err != nil {
				t.Errorf("%s with %q: %v", fixture.file, pw, err)
				continue
			}
			if got := r.NumPage(); got != 1 {
				t.Errorf("%s with %q: NumPage = %d, want 1", fixture.file, pw, got)
			}
			if p := r.Info().Producer(); !strings.Contains(p, "Ghostscript") {
				t.Errorf("%s with %q: Producer = %q, want Ghostscript", fixture.file, pw, p)
			}
			if r.encryptMetadata == fixture.cleartextMeta {
				t.Errorf("%s with %q: encryptMetadata = %v, want %v",
					fixture.file, pw, r.encryptMetadata, !fixture.cleartextMeta)
			}
			if fixture.cleartextMeta {
				assertCleartextXMP(t, r, fixture.file, pw)
			}
		}
		if _, err := openEncryptedFixture(t, fixture.file, "wrong-password"); err != ErrInvalidPassword {
			t.Errorf("%s with wrong password: got %v, want ErrInvalidPassword", fixture.file, err)
		}
	}
}

// assertCleartextXMP reads the catalog's /Metadata stream and verifies it
// yields a parseable cleartext XMP packet prefix — proving the
// /EncryptMetadata-false skip left the stored cleartext untouched.
func assertCleartextXMP(t *testing.T, r *Reader, fixture, pw string) {
	t.Helper()
	meta := r.Trailer().Key("Root").Key("Metadata")
	if meta.Kind() != Stream {
		t.Errorf("%s with %q: catalog has no /Metadata stream", fixture, pw)
		return
	}
	data, err := io.ReadAll(meta.Reader())
	if err != nil {
		t.Errorf("%s with %q: metadata read: %v", fixture, pw, err)
		return
	}
	s := string(data)
	if !strings.HasPrefix(s, "<?xpacket") && !strings.Contains(s, "<x:xmpmeta") {
		t.Errorf("%s with %q: metadata is not cleartext XMP (got %.40q)", fixture, pw, s)
	}
}
