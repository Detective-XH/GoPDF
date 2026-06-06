package pdf

import "fmt"

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
// malformed and must fail closed: treating it as /Identity would let encrypted
// data pass through undecrypted.
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
