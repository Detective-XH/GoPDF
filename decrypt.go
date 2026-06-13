package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rc4"
	"fmt"
	"io"
)

// cryptKey derives the per-object key (PDF 32000-1:2008 §7.6.2 Algorithm 1).
// AESV3 uses the file key directly; Identity never reaches here because
// decryptString and decryptStream switch on mode first.
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

// validAESKeyLen reports whether n is a valid AES key length in bytes
// (128/192/256-bit). AES.NewCipher rejects any other length; decryptAES screens
// the derived per-object key against it so a crafted /Length (e.g. 40 → a 5-byte
// V<=4 key) degrades to empty plaintext instead of reaching the cipher.
func validAESKeyLen(n int) bool {
	return n == 16 || n == 24 || n == 32
}

// decryptAES decrypts an AES-CBC payload: data = [BlockSize IV] || [PKCS7-padded ciphertext].
// Modifies data in-place. Returns nil on any validation or cipher error.
func decryptAES(key, data []byte) []byte {
	if !validAESKeyLen(len(key)) {
		return nil
	}
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
	// Verify every PKCS#7 padding byte equals pad in constant time, rejecting
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

// decryptStream reads AES streams into bounded memory so padding can be
// validated as one ciphertext block sequence. RC4 streams remain streaming
// because they do not need final-block validation.
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
