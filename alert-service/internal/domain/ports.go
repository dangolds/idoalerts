// Package-level ports for the alert-management service.
//
// Interfaces live in domain so service depends on domain, and infrastructure
// satisfies them from the outside (hexagonal / ports-and-adapters). Go
// interface satisfaction is structural — no implements keyword on impls.
// See DesignAndBreakdown.md §4 for the wider rationale.

package domain

import "context"

// AlertRepository persists and retrieves Alert aggregates.
//
// Contract that every implementation must honor:
//   - Create is for new alerts only; Update requires an existing row.
//   - Cross-tenant FindByID and Update collapse to ErrNotFound at the repo
//     boundary (§2.3, §2.8a). Never leak cross-tenant existence to callers;
//     ErrTenantMismatch is an internal-only sentinel.
//
// Impl-specific concerns (in-memory ordering, slice-vs-channel return shape,
// clone-on-read/write) are documented on the implementation, not here.
type AlertRepository interface {
	Create(ctx context.Context, a *Alert) error
	FindByID(ctx context.Context, tenantID, id string) (*Alert, error)
	List(ctx context.Context, f ListFilter) ([]*Alert, error)
	Update(ctx context.Context, a *Alert) error
}

// ListFilter is the query shape for AlertRepository.List.
// Pointers disambiguate "unset" from "status=OPEN" / "minScore=0.0" —
// both of which are valid filter values with non-zero meaning.
type ListFilter struct {
	TenantID string   // required
	Status   *Status  // optional — nil means no status filter
	MinScore *float64 // optional — nil means no score filter
}

// EventPublisher emits domain events to a downstream bus.
// Parameter type is Event (the marker interface, §9.2) — not any —
// so only typed domain events can be published.
type EventPublisher interface {
	Publish(ctx context.Context, event Event) error
}
