package domain

// Event is the marker interface every publishable domain event implements.
// Publisher takes Event, not any, so arbitrary structs cannot be published.
type Event interface {
	EventName() string
}

// AlertDecidedEvent fires after a decision is persisted.
type AlertDecidedEvent struct {
	Event string `json:"event"` // always "alert.decided"
	// json key is "decision" per PRD — do NOT rename to "status"
	AlertID   string `json:"alertId"`
	TenantID  string `json:"tenantId"`
	Decision  string `json:"decision"`
	Timestamp string `json:"timestamp"` // RFC3339 string, populated at publish time in service — do NOT switch to time.Time
}

func (e AlertDecidedEvent) EventName() string { return e.Event }

// AlertEscalatedEvent fires after an escalation is persisted.
type AlertEscalatedEvent struct {
	Event     string `json:"event"` // always "alert.escalated"
	AlertID   string `json:"alertId"`
	TenantID  string `json:"tenantId"`
	Timestamp string `json:"timestamp"` // RFC3339 string, populated at publish time in service — do NOT switch to time.Time
}

func (e AlertEscalatedEvent) EventName() string { return e.Event }
