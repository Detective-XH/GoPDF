package pdf

import "errors"

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
		// The password failed as the user password; try it as the owner password
		// (PDF 32000-1:2008 §7.6.3.4 Algorithm 7).
		if key, err = ownerAuthKey(password, O, U, P, n, R, ID, em); err != nil {
			return err
		}
	}
	r.key = key
	r.stmMode, r.strMode = stm, str
	r.encryptMetadata = em
	return nil
}

var ErrInvalidPassword = errors.New("encrypted PDF: invalid password")
