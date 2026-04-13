// Package events is an infrastructure-layer adapter satisfying
// domain.EventPublisher by writing JSON-encoded events to os.Stdout.
//
// Stdout is the simulated message broker (§2.7a). Every Publish writes
// exactly one JSON line followed by a newline so a downstream consumer can
// `tail -f | jq .` without pollution. Logs do NOT flow through this package
// — slog is configured against os.Stderr in cmd/server/main.go (Story 16).
// Any log.Printf or default-handler slog call inside internal/events would
// contaminate the broker stream; treat the no-logging rule as an enforced
// invariant on the whole package, not just a convention on this file.
//
// The underlying io.Writer is intentionally unbuffered. If a future change
// wraps os.Stdout in bufio.Writer for throughput (e.g. Story 17), the
// Flush MUST sit inside the same critical section as the Encode so a
// concurrent Publish cannot interleave with the tail of a pending
// buffered event — this prescribes the invariant, not a specific call
// sequence, so a wrapper that owns Flush elsewhere still holds it.
package events

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"

	"github.com/dangolds/idoalerts/alert-service/internal/domain"
)

// Compile-time assertion that StdoutPublisher satisfies the port.
var _ domain.EventPublisher = (*StdoutPublisher)(nil)

// StdoutPublisher writes domain events as newline-delimited JSON to an
// io.Writer fixed at construction.
type StdoutPublisher struct {
	// mu serializes Encode calls. json.Encoder.Encode is not documented as
	// goroutine-safe; concurrent writes on the shared writer can interleave
	// bytes. POSIX guarantees atomicity only for pipe writes <= PIPE_BUF,
	// and Windows WriteFile makes no atomicity guarantee at all. Story 17
	// wires this behind concurrent HTTP handlers (one goroutine per
	// request) so concurrent Publish is the default — a torn JSON line
	// silently breaks downstream `tail -f | jq` consumers, the exact
	// failure §2.7a's clean-stream invariant exists to prevent.
	mu sync.Mutex
	w  io.Writer
}

// NewStdoutPublisher returns a StdoutPublisher wired to os.Stdout.
// The writer is unexported and has no setter: stdout is the event bus
// by design, and a test-time writer seam would invite a second call site
// that bypasses the §2.7a invariant.
func NewStdoutPublisher() *StdoutPublisher {
	return &StdoutPublisher{w: os.Stdout}
}

// Publish JSON-encodes event and writes it as a single newline-terminated
// line. The newline is produced by json.Encoder.Encode itself — do not
// append another one or the broker stream gets double-spaced.
//
// The json.Encoder is constructed per call on purpose. Caching it as a
// struct field would save one allocation per event at the cost of making
// the struct carry a stale encoder if the writer ever changed; per-call
// keeps `w` the single source of truth and the struct minimal.
//
// ctx is accepted to honor the EventPublisher port but is not used — once
// the caller has reached this method, persist-before-publish (§2.7) has
// already committed, and cancelling the write post-commit would produce a
// silent divergence between storage state and the event stream.
func (p *StdoutPublisher) Publish(ctx context.Context, event domain.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return json.NewEncoder(p.w).Encode(event)
}
