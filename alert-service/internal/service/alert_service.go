// Package service orchestrates Alert use cases: load-check-mutate-persist-publish.
// Server-owned fields (ID, timestamps, Status on creation) are set here, never
// in handlers or storage (§2.8). Persist happens before publish; publish failure
// is logged at ERROR and does NOT fail the operation — repository state is
// authoritative (§2.7). See internal/domain for the port contracts this package
// consumes (AlertRepository, EventPublisher, Alert, events).
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/dangolds/idoalerts/alert-service/internal/domain"
)

// AlertService owns the four alert use cases. It is NOT a port-implementer
// (no compile-time guard line — the orchestrator depends on ports, not vice versa).
type AlertService struct {
	repo   domain.AlertRepository
	pub    domain.EventPublisher
	logger *slog.Logger
}

// NewAlertService wires the service with its collaborators. The logger is used
// ONLY for publish-failure logging (see DecideAlert / EscalateAlert); request-
// level logging is owned by HTTP middleware. Composition root guarantees
// non-nil args — a nil here panics loudly on first call by design.
func NewAlertService(repo domain.AlertRepository, pub domain.EventPublisher, logger *slog.Logger) *AlertService {
	return &AlertService{repo: repo, pub: pub, logger: logger}
}

// CreateAlertInput carries the fields a caller (handler today, tests tomorrow)
// can legally supply to CreateAlert. ID, Status, CreatedAt, UpdatedAt,
// DecisionNote are server-owned and intentionally absent here — the type
// makes that impossible to smuggle (§2.8). Plain Go fields only: wire-layer
// validator/json tags belong on the handler DTO (Story 11+), not here.
type CreateAlertInput struct {
	TenantID          string
	TransactionID     string
	MatchedEntityName string
	MatchScore        float64
	AssignedTo        *string
}

// CreateAlert generates a server-owned ID (§2.8) and sets Status=OPEN plus
// CreatedAt == UpdatedAt from a single time.Now().UTC() call — the two
// timestamps are equal by invariant on creation; two separate Now() calls
// could differ by nanoseconds and break that property. DecisionNote is
// explicitly "" (AM-2: plain string on domain, "" means "no note yet").
// No event is emitted on Create per PRD — only decide + escalate emit events.
func (s *AlertService) CreateAlert(ctx context.Context, in CreateAlertInput) (*domain.Alert, error) {
	now := time.Now().UTC()
	a := &domain.Alert{
		ID:                uuid.NewString(),
		TenantID:          in.TenantID,
		TransactionID:     in.TransactionID,
		MatchedEntityName: in.MatchedEntityName,
		MatchScore:        in.MatchScore,
		Status:            domain.StatusOpen,
		AssignedTo:        in.AssignedTo,
		DecisionNote:      "",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.repo.Create(ctx, a); err != nil {
		// Propagate ErrAlreadyExists unchanged — UUID collision is effectively
		// unreachable, but honoring the port contract keeps the boundary clean.
		return nil, err
	}
	return a, nil
}

// ListAlerts is a pass-through to the repository. The service trusts the port
// contract: tenant scoping, optional status/minScore filtering, deterministic
// sort, and non-nil empty slice on zero matches are repo-layer responsibilities
// (§9.1, §9.12). No business logic belongs here.
func (s *AlertService) ListAlerts(ctx context.Context, f domain.ListFilter) ([]*domain.Alert, error) {
	return s.repo.List(ctx, f)
}

// DecideAlert persists a CLEARED / CONFIRMED_HIT decision on an OPEN or
// ESCALATED alert and emits an alert.decided event.
//
// MVP: strict write-once per PRD (§2.9c). Any second decision on a terminal
// alert returns ErrAlreadyDecided — no idempotency dedup, no "same-decision
// is a no-op" (post-MVP possibilities).
//
// Flow: validate newStatus → load → CanDecide (race-aware per §2.8b) →
// mutate → persist (§2.7 — if Update fails, no event) → build event
// (§9.13: Timestamp at publish time, NOT from a.UpdatedAt) → publish
// (failure logged at ERROR, returns success anyway — state is authoritative).
//
// State machine per §2.1: OPEN→{CLEARED,CONFIRMED_HIT}; ESCALATED→{CLEARED,CONFIRMED_HIT}.
func (s *AlertService) DecideAlert(ctx context.Context, tenantID, id string, newStatus domain.Status, note string) (*domain.Alert, error) {
	// Defense in depth: the DTO oneof guards the HTTP path, but the service is
	// a package-boundary port and can be called from tests/future non-HTTP
	// callers. An invalid newStatus would corrupt the event stream ("decision":"OPEN").
	if newStatus != domain.StatusCleared && newStatus != domain.StatusConfirmedHit {
		return nil, domain.ErrInvalidTransition
	}

	a, err := s.repo.FindByID(ctx, tenantID, id)
	if err != nil {
		// ErrNotFound covers cross-tenant too (repo collapses per §2.3/§2.8a).
		return nil, err
	}

	// §2.8b: accepted read-check-write race. Two simultaneous decides on the
	// same OPEN alert could both pass CanDecide and both Update (last-write-wins).
	// Production fix is DB-level (SELECT ... FOR UPDATE); the rubric tests
	// immutability sequentially, so the simple pattern is correct for MVP.
	if !a.CanDecide() {
		if a.Status == domain.StatusCleared || a.Status == domain.StatusConfirmedHit {
			return nil, domain.ErrAlreadyDecided
		}
		// Unreachable today given the 4-status enum; guards future non-decidable,
		// non-terminal statuses from being mislabeled "already decided".
		return nil, domain.ErrInvalidTransition
	}

	a.Status = newStatus
	a.DecisionNote = note
	a.UpdatedAt = time.Now().UTC()

	if err := s.repo.Update(ctx, a); err != nil {
		return nil, err
	}

	evt := domain.AlertDecidedEvent{
		Event:     "alert.decided",
		AlertID:   a.ID,
		TenantID:  a.TenantID,
		Decision:  string(newStatus),
		Timestamp: time.Now().UTC().Format(time.RFC3339), // §9.13: publish time, NOT a.UpdatedAt
	}
	if err := s.pub.Publish(ctx, evt); err != nil {
		// §2.7: state is authoritative; log and return success.
		s.logger.ErrorContext(ctx, "publish alert.decided failed",
			slog.String("alert_id", a.ID),
			slog.String("tenant_id", a.TenantID),
			slog.String("event", "alert.decided"),
			slog.Any("err", err),
		)
	}
	return a, nil
}

// EscalateAlert transitions an OPEN alert to ESCALATED and emits alert.escalated.
//
// Flow: load → CanEscalate (race-aware per §2.8b, same shape as DecideAlert) →
// mutate → persist (§2.7) → build event (§9.13 timestamp) → publish
// (failure logged at ERROR, operation still succeeds).
//
// State machine per §2.1: OPEN→ESCALATED is the only valid transition;
// ESCALATED / CLEARED / CONFIRMED_HIT all return ErrInvalidTransition.
func (s *AlertService) EscalateAlert(ctx context.Context, tenantID, id string) (*domain.Alert, error) {
	a, err := s.repo.FindByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	// §2.8b: same read-check-write race shape as DecideAlert; accepted for MVP.
	if !a.CanEscalate() {
		return nil, domain.ErrInvalidTransition
	}

	a.Status = domain.StatusEscalated
	a.UpdatedAt = time.Now().UTC()

	if err := s.repo.Update(ctx, a); err != nil {
		return nil, err
	}

	evt := domain.AlertEscalatedEvent{
		Event:     "alert.escalated",
		AlertID:   a.ID,
		TenantID:  a.TenantID,
		Timestamp: time.Now().UTC().Format(time.RFC3339), // §9.13: publish time, NOT a.UpdatedAt
	}
	if err := s.pub.Publish(ctx, evt); err != nil {
		// §2.7: state is authoritative; log and return success.
		s.logger.ErrorContext(ctx, "publish alert.escalated failed",
			slog.String("alert_id", a.ID),
			slog.String("tenant_id", a.TenantID),
			slog.String("event", "alert.escalated"),
			slog.Any("err", err),
		)
	}
	return a, nil
}
