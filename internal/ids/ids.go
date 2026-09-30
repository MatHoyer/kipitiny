// Package ids generates sortable, portable identifiers (ULIDs stored as TEXT).
package ids

import "github.com/oklog/ulid/v2"

func New() string {
	return ulid.Make().String()
}
