package domain

// Event is the marker interface every publishable domain event implements.
// Publisher takes Event, not any, so arbitrary structs cannot be published.
type Event interface {
	EventName() string
}

const (
	EventNameAlertDecided   = "alert.decided"
	EventNameAlertEscalated = "alert.escalated"
)

// AlertDecidedEvent fires after a decision is persisted.
type AlertDecidedEvent struct {
	Event string `json:"event"` // always "alert.decided"
	// json key is "decision" per PRD — do NOT rename to "status"
	AlertID   string `json:"alertId"`
	TenantID  string `json:"tenantId"`
	Decision  string `json:"decision"`
	Timestamp string `json:"timestamp"` // RFC3339 string, populated at publish time in service — do NOT switch to time.Time
}

// EventName returns the literal const, not e.Event — compile-time constant
// so type-identity stays correct even if the wire field is mis-populated.
func (AlertDecidedEvent) EventName() string { return EventNameAlertDecided }

// AlertEscalatedEvent fires after an escalation is persisted.
type AlertEscalatedEvent struct {
	Event     string `json:"event"` // always "alert.escalated"
	AlertID   string `json:"alertId"`
	TenantID  string `json:"tenantId"`
	Timestamp string `json:"timestamp"` // RFC3339 string, populated at publish time in service — do NOT switch to time.Time
}

func (AlertEscalatedEvent) EventName() string { return EventNameAlertEscalated }
