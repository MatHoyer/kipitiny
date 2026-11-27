package domain

import "errors"

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrInvalid  = errors.New("invalid input")
	// ErrBusy means another operation (usually a deploy) holds the service.
	ErrBusy = errors.New("operation in progress")
	// ErrShuttingDown is returned for work submitted during shutdown.
	ErrShuttingDown = errors.New("manager is shutting down")
)
