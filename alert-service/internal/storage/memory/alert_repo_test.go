// Black-box tests for the in-memory AlertRepository implementation.
//
// The suite lives in package memory_test so it consumes only the exported
// port surface — the same way the service layer will. It proves the three
// impl invariants documented on alert_repo.go's package doc:
//   - clone-on-read AND clone-on-write at the lock boundary (§2.8a)
//   - List returns a non-nil empty slice on zero matches (§9.1)
//   - List is sorted CreatedAt desc and filters before sorting (§9.12)
// plus the port-contract rules: cross-tenant collapse to ErrNotFound,
// and Create collision → ErrAlreadyExists.
package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dangolds/idoalerts/alert-service/internal/domain"
	"github.com/dangolds/idoalerts/alert-service/internal/storage/memory"
)

// newAlert is a test-only builder. Kept in _test.go per project rule: no
// exported testutil package — Story 10 must not pick up a test-only import
// dependency.
func newAlert(id, tenantID string, opts ...func(*domain.Alert)) *domain.Alert {
	a := &domain.Alert{
		ID:                id,
		TenantID:          tenantID,
		TransactionID:     "tx-" + id,
		MatchedEntityName: "ACME Sanctioned Corp",
		MatchScore:        50,
		Status:            domain.StatusOpen,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func withCreatedAt(t time.Time) func(*domain.Alert) {
	return func(a *domain.Alert) { a.CreatedAt = t }
}

func withStatus(s domain.Status) func(*domain.Alert) {
	return func(a *domain.Alert) { a.Status = s }
}

func withMatchScore(s float64) func(*domain.Alert) {
	return func(a *domain.Alert) { a.MatchScore = s }
}

func withAssignedTo(name string) func(*domain.Alert) {
	return func(a *domain.Alert) { a.AssignedTo = &name }
}

func ptrFloat(f float64) *float64 { return &f }

// --- CRUD round-trip ------------------------------------------------------

func TestAlertRepo_CreateFindUpdateRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAlertRepo()

	created := newAlert("a1", "t1", withAssignedTo("alice"))
	if err := repo.Create(ctx, created); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.FindByID(ctx, "t1", "a1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ID != "a1" || got.TenantID != "t1" || got.Status != domain.StatusOpen {
		t.Fatalf("FindByID returned wrong values: %+v", got)
	}
	if got.AssignedTo == nil || *got.AssignedTo != "alice" {
		t.Fatalf("AssignedTo not round-tripped: %+v", got.AssignedTo)
	}

	toUpdate := newAlert("a1", "t1", withStatus(domain.StatusCleared))
	toUpdate.DecisionNote = "false positive"
	if err := repo.Update(ctx, toUpdate); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got2, err := repo.FindByID(ctx, "t1", "a1")
	if err != nil {
		t.Fatalf("FindByID after Update: %v", err)
	}
	if got2.Status != domain.StatusCleared || got2.DecisionNote != "false positive" {
		t.Fatalf("Update did not round-trip: %+v", got2)
	}
	// Belt-and-braces: repo returned a clone of its stored pointer, not the
	// caller's pointer passed to Update.
	if got2 == toUpdate {
		t.Fatalf("FindByID returned the same pointer passed to Update — clone boundary broken")
	}
}

// --- Cross-tenant collapse ------------------------------------------------

func TestAlertRepo_FindByID_CrossTenantReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAlertRepo()

	if err := repo.Create(ctx, newAlert("a1", "t1")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := repo.FindByID(ctx, "t2", "a1")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant FindByID: want ErrNotFound, got %v", err)
	}
	// Defense in depth — repo must NEVER surface ErrTenantMismatch to callers
	// (§2.3 / §2.8a — internal-only sentinel).
	if errors.Is(err, domain.ErrTenantMismatch) {
		t.Fatalf("cross-tenant FindByID leaked ErrTenantMismatch — existence-leak regression")
	}
}

func TestAlertRepo_Update_CrossTenantReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAlertRepo()

	if err := repo.Create(ctx, newAlert("a1", "t1", withStatus(domain.StatusOpen))); err != nil {
		t.Fatalf("Create: %v", err)
	}

	intruder := newAlert("a1", "t2", withStatus(domain.StatusCleared))
	err := repo.Update(ctx, intruder)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant Update: want ErrNotFound, got %v", err)
	}

	// Prove the store wasn't touched.
	got, err := repo.FindByID(ctx, "t1", "a1")
	if err != nil {
		t.Fatalf("FindByID after cross-tenant Update: %v", err)
	}
	if got.Status != domain.StatusOpen {
		t.Fatalf("cross-tenant Update mutated store: status=%v", got.Status)
	}
}

// --- Create collision -----------------------------------------------------

func TestAlertRepo_Create_DuplicateIDReturnsAlreadyExists(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAlertRepo()

	original := newAlert("a1", "t1", withMatchScore(42))
	if err := repo.Create(ctx, original); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	dup := newAlert("a1", "t1", withMatchScore(99))
	err := repo.Create(ctx, dup)
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate Create: want ErrAlreadyExists, got %v", err)
	}

	// No silent overwrite.
	got, err := repo.FindByID(ctx, "t1", "a1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.MatchScore != 42 {
		t.Fatalf("duplicate Create overwrote store: MatchScore=%v", got.MatchScore)
	}
}

// --- Clone-on-read (§2.8a) ------------------------------------------------

func TestAlertRepo_FindByID_CallerMutationDoesNotAffectStore(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAlertRepo()

	if err := repo.Create(ctx, newAlert("a1", "t1",
		withStatus(domain.StatusOpen),
		withMatchScore(80),
		withAssignedTo("alice"),
	)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.FindByID(ctx, "t1", "a1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}

	// Mutate every boundary we care about, incl. the only non-primitive in
	// Clone today (*AssignedTo).
	got.Status = domain.StatusCleared
	got.MatchScore = 0
	*got.AssignedTo = "mallory"

	again, err := repo.FindByID(ctx, "t1", "a1")
	if err != nil {
		t.Fatalf("FindByID after mutation: %v", err)
	}
	if again.Status != domain.StatusOpen || again.MatchScore != 80 {
		t.Fatalf("caller mutation leaked into store: %+v", again)
	}
	if again.AssignedTo == nil || *again.AssignedTo != "alice" {
		t.Fatalf("caller mutation leaked through *AssignedTo: %v", again.AssignedTo)
	}
}

// --- Clone-on-write (§2.8a bilateral) -------------------------------------
//
// Beyond the story's stated AC (which only names clone-on-read). §2.8a is a
// bilateral invariant — the write-side clone call site must be proven too,
// otherwise a future refactor could store the caller's pointer directly and
// the read-side clone alone wouldn't catch the corruption.

func TestAlertRepo_Update_CallerMutationAfterUpdateDoesNotAffectStore(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAlertRepo()

	if err := repo.Create(ctx, newAlert("a1", "t1", withStatus(domain.StatusOpen))); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated := newAlert("a1", "t1", withStatus(domain.StatusCleared))
	updated.DecisionNote = "cleared"
	if err := repo.Update(ctx, updated); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Mutate the caller's pointer post-Update. If the repo stored it directly
	// (broken clone-on-write) this would bleed into the next FindByID.
	updated.Status = domain.StatusOpen
	updated.DecisionNote = "MUTATED"

	got, err := repo.FindByID(ctx, "t1", "a1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Status != domain.StatusCleared || got.DecisionNote != "cleared" {
		t.Fatalf("post-Update caller mutation leaked into store: %+v", got)
	}
}

// --- Clone-on-read for List (§2.8a, gap N2) -------------------------------

func TestAlertRepo_List_CallerMutationDoesNotAffectStore(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAlertRepo()

	if err := repo.Create(ctx, newAlert("a1", "t1", withAssignedTo("alice"))); err != nil {
		t.Fatalf("Create a1: %v", err)
	}
	if err := repo.Create(ctx, newAlert("a2", "t1", withAssignedTo("bob"))); err != nil {
		t.Fatalf("Create a2: %v", err)
	}

	got, err := repo.List(ctx, domain.ListFilter{TenantID: "t1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List: want 2, got %d", len(got))
	}

	// Mutate both primitive + pointer-backed fields on both entries.
	got[0].Status = domain.StatusCleared
	*got[0].AssignedTo = "mallory"
	got[1].MatchScore = -1

	for _, id := range []string{"a1", "a2"} {
		a, err := repo.FindByID(ctx, "t1", id)
		if err != nil {
			t.Fatalf("FindByID %s: %v", id, err)
		}
		if a.Status != domain.StatusOpen {
			t.Fatalf("List-returned pointer mutation leaked Status for %s: %v", id, a.Status)
		}
		if a.MatchScore != 50 {
			t.Fatalf("List-returned pointer mutation leaked MatchScore for %s: %v", id, a.MatchScore)
		}
	}
	a1, _ := repo.FindByID(ctx, "t1", "a1")
	if a1.AssignedTo == nil || *a1.AssignedTo != "alice" {
		t.Fatalf("List-returned *AssignedTo mutation leaked: %v", a1.AssignedTo)
	}
}

// --- List shape -----------------------------------------------------------

func TestAlertRepo_List_EmptyReturnsNonNilSlice(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAlertRepo()

	got, err := repo.List(ctx, domain.ListFilter{TenantID: "t1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Two distinct invariants (§9.1):
	//  - len == 0 proves filtering excluded everything.
	//  - json.Marshal == "[]" proves the slice is non-nil — a nil slice would
	//    marshal to "null" and silently break the {"alerts": []} wire contract.
	// The JSON check is the one that catches a nil return; the length check
	// alone is not enough.
	if len(got) != 0 {
		t.Fatalf("empty List: want len 0, got %d", len(got))
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(b) != "[]" {
		t.Fatalf("empty List JSON: want %q, got %q", "[]", string(b))
	}
}

func TestAlertRepo_List_SortedByCreatedAtDesc(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAlertRepo()

	// Explicit CreatedAt offsets dodge the Windows 100ns timer granularity
	// (§9.12 / Story 6 notes) — two back-to-back time.Now() calls can return
	// equal values there, which would make this assertion rely on
	// sort.SliceStable's insertion-order fallback instead of the actual sort.
	now := time.Now().UTC()
	if err := repo.Create(ctx, newAlert("oldest", "t1", withCreatedAt(now.Add(-30*time.Second)))); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, newAlert("middle", "t1", withCreatedAt(now.Add(-15*time.Second)))); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, newAlert("newest", "t1", withCreatedAt(now))); err != nil {
		t.Fatal(err)
	}

	got, err := repo.List(ctx, domain.ListFilter{TenantID: "t1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	want := []string{"newest", "middle", "oldest"}
	for i, w := range want {
		if got[i].ID != w {
			t.Fatalf("sort order at index %d: want %s, got %s", i, w, got[i].ID)
		}
	}
}

func TestAlertRepo_List_FiltersByStatusAndMinScore(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAlertRepo()

	// One row per exclusion reason + one positive row. Each predicate branch
	// has a dedicated "must-be-excluded" guard: a bug that short-circuits
	// any single filter still trips exactly one row.
	seeds := []*domain.Alert{
		newAlert("positive", "t1", withStatus(domain.StatusOpen), withMatchScore(95)),
		newAlert("wrong-tenant", "t2", withStatus(domain.StatusOpen), withMatchScore(95)),   // excluded by tenant
		newAlert("wrong-status", "t1", withStatus(domain.StatusCleared), withMatchScore(95)), // excluded by status
		newAlert("below-score", "t1", withStatus(domain.StatusOpen), withMatchScore(50)),    // excluded by minScore
	}
	for _, s := range seeds {
		if err := repo.Create(ctx, s); err != nil {
			t.Fatalf("Create %s: %v", s.ID, err)
		}
	}

	open := domain.StatusOpen
	got, err := repo.List(ctx, domain.ListFilter{
		TenantID: "t1",
		Status:   &open,
		MinScore: ptrFloat(90),
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "positive" {
		ids := make([]string, len(got))
		for i, a := range got {
			ids[i] = a.ID
		}
		t.Fatalf("filter composition: want [positive], got %v", ids)
	}
}

// --- Concurrency (§2.8b pointer comment — non-negotiable) -----------------

// TestAlertRepo_ConcurrentCreateFindUpdate_RaceClean spams the repo with
// heterogeneous parallel ops to prove the RWMutex + Clone pattern is
// heap-safe.
//
// §2.8b — this test proves HEAP SAFETY of the RWMutex + Clone pattern under
// concurrent goroutines. It does NOT assert business-rule atomicity
// (no double-decide, no torn writes at the domain level): that gap is the
// service layer's concern (read-check-write for decisions) and is an
// accepted MVP limitation — the production fix is DB-level SELECT ... FOR
// UPDATE, not application code. Run with `go test -race` to validate;
// without -race the test still passes but its point is moot.
//
// The workload is partitioned per operation so each goroutine has a clear
// expectation of "must return nil error" — no blanket tolerance bucket for
// ErrNotFound / ErrAlreadyExists, because those cannot legally occur in
// this constructed workload and tolerating them would mask a
// lock-corruption bug on the Update path.
func TestAlertRepo_ConcurrentCreateFindUpdate_RaceClean(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAlertRepo()

	// Seed a fixed pool of IDs SERIALLY before spinning goroutines. Readers
	// and updaters target only these IDs — they are guaranteed to exist, so
	// FindByID and Update must always return nil.
	const sharedPoolSize = 32
	sharedIDs := make([]string, sharedPoolSize)
	for i := range sharedIDs {
		id := fmt.Sprintf("shared-%d", i)
		sharedIDs[i] = id
		if err := repo.Create(ctx, newAlert(id, "t1", withStatus(domain.StatusOpen))); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	const goroutines = 50
	const iters = 100

	var (
		wg     sync.WaitGroup
		errMu  sync.Mutex
		errs   []error
	)
	recordErr := func(err error) {
		errMu.Lock()
		errs = append(errs, err)
		errMu.Unlock()
	}

	// Creates — FRESH UUID per call. Collisions are cryptographically
	// impossible, so every call must return nil.
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				id := uuid.NewString()
				a := newAlert(id, "t1", withStatus(domain.StatusOpen))
				if err := repo.Create(ctx, a); err != nil {
					recordErr(fmt.Errorf("Create(%s): %w", id, err))
				}
			}
		}()
	}

	// Readers — targets only shared-pool IDs that were seeded above. Every
	// call must return nil.
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				id := sharedIDs[(g*iters+i)%sharedPoolSize]
				if _, err := repo.FindByID(ctx, "t1", id); err != nil {
					recordErr(fmt.Errorf("FindByID(%s): %w", id, err))
				}
			}
		}(g)
	}

	// Updaters — targets only shared-pool IDs that were seeded above. Every
	// call must return nil. Writes a goroutine-specific DecisionNote so the
	// race detector has something meaningful to inspect if the lock breaks.
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				id := sharedIDs[(g*iters+i)%sharedPoolSize]
				a := newAlert(id, "t1", withStatus(domain.StatusOpen))
				a.DecisionNote = fmt.Sprintf("g%d-i%d", g, i)
				if err := repo.Update(ctx, a); err != nil {
					recordErr(fmt.Errorf("Update(%s): %w", id, err))
				}
			}
		}(g)
	}

	wg.Wait()

	if len(errs) > 0 {
		// Surface up to 5 — the rest are likely the same bug.
		lim := len(errs)
		if lim > 5 {
			lim = 5
		}
		for _, e := range errs[:lim] {
			t.Errorf("concurrent op: %v", e)
		}
		t.Fatalf("%d concurrent ops failed; all ops on seeded IDs / fresh UUIDs must succeed", len(errs))
	}
}

// --- Port contract for a future DB impl (§2.2) ----------------------------
//
// Placeholder that materializes the ctx-cancellation contract in code for
// a future DB adapter to adopt. The in-memory impl legitimately ignores ctx
// (no I/O to cancel) — skipping keeps this visible as a documented gap
// without greenlighting a no-op assertion today.
func TestAlertRepo_ContextCancellation_ContractForDBImpl(t *testing.T) {
	t.Skip("§2.2 — in-memory impl ignores ctx for MVP; contract-level test lands with DB impl")
}
