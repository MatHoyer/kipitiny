package domain

import "io"

// Encryption seals backups to a target's public key and opens them with its
// private key (age X25519 keys: AGE-SECRET-KEY-1… and age1…). Errors are
// the implementation's, unwrapped.
type Encryption interface {
	// NewKey returns a new private key and its public recipient.
	NewKey() (identity, recipient string, err error)
	// ParseIdentity checks a private key, returning it in canonical form
	// and its recipient.
	ParseIdentity(identity string) (canonical, recipient string, err error)
	// Encrypt returns a writer sealing to recipient; Close writes the
	// final chunk.
	Encrypt(w io.Writer, recipient string) (io.WriteCloser, error)
	// Decrypt opens what Encrypt sealed for identity's recipient.
	Decrypt(r io.Reader, identity string) (io.Reader, error)
}
