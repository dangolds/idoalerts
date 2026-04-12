package domain

import "errors"

var (
	ErrNotFound          = errors.New("alert not found")
	ErrAlreadyDecided    = errors.New("alert has already been decided")
	ErrInvalidTransition = errors.New("invalid state transition")

	// ErrTenantMismatch signals that a lookup matched by ID but the tenant does
	// not own the resource. Never surfaced to clients — the repository collapses
	// this to ErrNotFound at its boundary (§2.3, §2.8a) so cross-tenant existence
	// is not leaked. Kept as a distinct internal sentinel to enable disambiguated
	// logging or future policy hooks without re-threading the repo API.
	ErrTenantMismatch = errors.New("tenant mismatch")
)
