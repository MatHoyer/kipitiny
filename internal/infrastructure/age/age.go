// Package age is domain.Encryption with age (filippo.io/age) X25519 keys.
package age

import (
	"io"

	"filippo.io/age"
)

// Encryption implements domain.Encryption.
type Encryption struct{}

func (Encryption) NewKey() (identity, recipient string, err error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "", err
	}
	return id.String(), id.Recipient().String(), nil
}

func (Encryption) ParseIdentity(identity string) (canonical, recipient string, err error) {
	id, err := age.ParseX25519Identity(identity)
	if err != nil {
		return "", "", err
	}
	return id.String(), id.Recipient().String(), nil
}

func (Encryption) Encrypt(w io.Writer, recipient string) (io.WriteCloser, error) {
	r, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return nil, err
	}
	return age.Encrypt(w, r)
}

func (Encryption) Decrypt(r io.Reader, identity string) (io.Reader, error) {
	id, err := age.ParseX25519Identity(identity)
	if err != nil {
		return nil, err
	}
	return age.Decrypt(r, id)
}
