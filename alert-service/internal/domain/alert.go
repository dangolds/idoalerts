package domain

import "time"

// Status is the lifecycle state of an Alert. Wire values match the PRD exactly.
type Status string

const (
	StatusOpen         Status = "OPEN"
	StatusEscalated    Status = "ESCALATED"
	StatusCleared      Status = "CLEARED"
	StatusConfirmedHit Status = "CONFIRMED_HIT"
)

// Alert is the sanctions-screening alert aggregate.
type Alert struct {
	ID                string
	TenantID          string
	TransactionID     string
	MatchedEntityName string
	MatchScore        float64
	Status            Status
	AssignedTo        *string
	// DecisionNote is plain string (not *string): "" means "no note yet".
	// DTO DecideRequest.DecisionNote is required — analysts must justify new
	// decisions — but the domain entity itself tolerates "". Do not "fix" to *string.
	DecisionNote string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CanDecide reports whether the alert is in a state that accepts a decision.
// OPEN or ESCALATED may be decided (per clarified state machine).
func (a *Alert) CanDecide() bool {
	return a.Status == StatusOpen || a.Status == StatusEscalated
}

// CanEscalate reports whether the alert may transition to ESCALATED.
// Only OPEN alerts can be escalated.
func (a *Alert) CanEscalate() bool {
	return a.Status == StatusOpen
}

// Clone returns a deep copy whose pointer fields do not alias the receiver's.
// Keep in sync: add explicit deep-copy logic here for any future slice, map,
// or pointer field added to Alert.
func (a *Alert) Clone() *Alert {
	cp := *a
	if a.AssignedTo != nil {
		v := *a.AssignedTo
		cp.AssignedTo = &v
	}
	return &cp
}
