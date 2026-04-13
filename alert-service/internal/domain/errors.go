package domain

import "errors"

var (
	ErrNotFound = errors.New("alert not found")

	// ErrAlreadyExists indicates a Create call collided with an existing alert ID.
	// The repo enforces the port's "Create is for new alerts only" contract;
	// silent overwrite would violate the port documentation committed in Story 5.
	// HTTP mapping (future Story 12): 409 ALERT_ALREADY_EXISTS.
	ErrAlreadyExists = errors.New("alert already exists")

	ErrAlreadyDecided    = errors.New("alert has already been decided")
	ErrInvalidTransition = errors.New("invalid state transition")

	// ErrTenantMismatch signals that a lookup matched by ID but the tenant does
	// not own the resource. Never surfaced to clients — the repository collapses
	// this to ErrNotFound at its boundary (§2.3, §2.8a) so cross-tenant existence
	// is not leaked. Kept as a distinct internal sentinel to enable disambiguated
	// logging or future policy hooks without re-threading the repo API.
	ErrTenantMismatch = errors.New("tenant mismatch")
)
