// Package memory is an in-memory implementation of domain.AlertRepository.
//
// Impl-flavored invariants (moved here from the port doc so future DB/channel
// impls aren't over-specified):
//
//  1. Clone-on-read/write (§2.8a). The map holds *domain.Alert; returning or
//     storing the raw pointer would let callers mutate locked state outside
//     the mutex. Every entry/exit of the lock boundary clones.
//  2. List non-nil empty slice (§9.1). Zero matches return
//     make([]*domain.Alert, 0), never nil — the service passes this straight
//     through to JSON and nil marshals to null, breaking the wire contract.
//  3. List sorted by CreatedAt descending (§9.12). Map iteration is randomized;
//     deterministic order is required for stable tests and future pagination.
//  4. Cross-tenant FindByID/Update collapse to ErrNotFound at this boundary
//     (port-contract restatement — security invariant, never leak existence).
//
// Atomicity of compound operations (read-check-write for decisions) is the
// service layer's concern; see DesignAndBreakdown §2.8b. This repo provides
// per-method atomicity only.
package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/dangolds/idoalerts/alert-service/internal/domain"
)

// Compile-time assertion that AlertRepo satisfies the port. Fails to build
// the moment the port shape drifts.
var _ domain.AlertRepository = (*AlertRepo)(nil)

// AlertRepo is a thread-safe in-memory AlertRepository.
type AlertRepo struct {
	mu     sync.RWMutex
	alerts map[string]*domain.Alert
}

// NewAlertRepo returns a ready-to-use repository with an initialized map.
func NewAlertRepo() *AlertRepo {
	return &AlertRepo{alerts: make(map[string]*domain.Alert)}
}

// Create stores a new alert. ID collisions return ErrAlreadyExists (port
// contract: Create is for new alerts only — callers with existing rows use
// Update). ctx is accepted to satisfy the interface; a future DB impl will
// use it for cancellation.
func (r *AlertRepo) Create(ctx context.Context, a *domain.Alert) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.alerts[a.ID]; exists {
		return domain.ErrAlreadyExists
	}
	r.alerts[a.ID] = a.Clone()
	return nil
}

// FindByID returns a clone of the alert scoped to tenantID. Missing id and
// cross-tenant reads both return ErrNotFound — never leak cross-tenant
// existence.
func (r *AlertRepo) FindByID(ctx context.Context, tenantID, id string) (*domain.Alert, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.alerts[id]
	if !ok || a.TenantID != tenantID {
		return nil, domain.ErrNotFound
	}
	return a.Clone(), nil
}

// List returns alerts matching f, scoped to f.TenantID, sorted CreatedAt desc.
// Empty result is a non-nil empty slice (§9.1).
func (r *AlertRepo) List(ctx context.Context, f domain.ListFilter) ([]*domain.Alert, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// pre-allocated so zero-match returns [], not null (§9.1)
	out := make([]*domain.Alert, 0)
	for _, a := range r.alerts {
		if a.TenantID != f.TenantID {
			continue
		}
		if f.Status != nil && a.Status != *f.Status {
			continue
		}
		if f.MinScore != nil && a.MatchScore < *f.MinScore {
			continue
		}
		out = append(out, a.Clone())
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// Update overwrites an existing alert. Missing id or cross-tenant target
// returns ErrNotFound. The stored value is a clone of a, so post-Update
// mutations by the caller do not alias repo state.
func (r *AlertRepo) Update(ctx context.Context, a *domain.Alert) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.alerts[a.ID]
	if !ok || existing.TenantID != a.TenantID {
		return domain.ErrNotFound
	}
	r.alerts[a.ID] = a.Clone()
	return nil
}
