package data

import (
	"errors"
	"fmt"
	"io"
)

// encryptTo seals what is written to w for recipient.
func (c *Data) encryptTo(w io.Writer, recipient string) (io.WriteCloser, error) {
	enc, err := c.crypt.Encrypt(w, recipient)
	if err != nil {
		return nil, fmt.Errorf("age recipient: %w", err)
	}
	return enc, nil
}

// decryptFrom opens r, sealed for the recipient of identity.
func (c *Data) decryptFrom(r io.Reader, identity string) (io.Reader, error) {
	if identity == "" {
		return nil, errors.New("backup is encrypted but its target has no key")
	}
	if _, _, err := c.crypt.ParseIdentity(identity); err != nil {
		return nil, fmt.Errorf("age identity: %w", err)
	}
	dec, err := c.crypt.Decrypt(r, identity)
	if err != nil {
		return nil, fmt.Errorf("decrypt backup: %w", err)
	}
	return dec, nil
}
