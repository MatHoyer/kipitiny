package core

import (
	"fmt"
	"io"

	"filippo.io/age"
)

func encryptTo(w io.Writer, recipient string) (io.WriteCloser, error) {
	r, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return nil, fmt.Errorf("age recipient: %w", err)
	}
	return age.Encrypt(w, r)
}

func decryptFrom(r io.Reader, identity string) (io.Reader, error) {
	if identity == "" {
		return nil, fmt.Errorf("backup is encrypted but its target has no key")
	}
	id, err := age.ParseX25519Identity(identity)
	if err != nil {
		return nil, fmt.Errorf("age identity: %w", err)
	}
	dec, err := age.Decrypt(r, id)
	if err != nil {
		return nil, fmt.Errorf("decrypt backup: %w", err)
	}
	return dec, nil
}

func newAgeKey() (identity, recipient string, err error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "", err
	}
	return id.String(), id.Recipient().String(), nil
}
