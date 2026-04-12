# Architectural Blueprint: Alert Management Service (MVP)

## 1. Overview & Architectural Principles

This document outlines the design and execution plan for the Alert Management Service MVP. The system is designed around **Clean Architecture** and **Repository Pattern** principles to ensure a strict separation of concerns, high testability, and a clear path to scale from an MVP to a production-ready system.

*   **SOLID & Single Responsibility:** HTTP handlers manage routing, deserialization, and validation. Services manage business logic and state enforcement. Repositories manage data access.
*   **Modularity:** Enforced via Go's internal package visibility rules.
*   **Dependency Injection:** Dependencies (Storage, Event Publishers) are injected into the Service layer at application startup (Composition Root).

---

## 2. Project Structure

The project will follow the standard Go directory layout to enforce architectural boundaries:

```text
/alert-service
├── cmd/
│   └── server/
│       └── main.go              # Entry point, Dependency Injection wiring, Server startup
├── internal/
│   ├── domain/                  # Core models, State Enums, Errors, and Interfaces (Zero dependencies)
│   ├── service/                 # Business logic, state machine enforcement
│   ├── storage/                 # In-memory repository implementation (Data Access Layer)
│   └── api/                     # HTTP Handlers, Routing, DTOs, and Request Validation
├── go.mod
└── go.sum
```

---

## 3. Domain Model & Business Rules

### Entity Definition
*   **ID**: UUID (String)
*   **TransactionID**: String
*   **MatchedEntityName**: String
*   **MatchScore**: Float (0.00 - 100.00)
*   **Status**: Enum (OPEN, ESCALATED, CLEARED, CONFIRMED_HIT)
*   **AssignedTo**: String (Nullable)
*   **TenantID**: String
*   **DecisionNote**: String (Nullable)
*   **CreatedAt**: Timestamp
*   **UpdatedAt**: Timestamp

### State Machine & Immutability Rules
Alert state transitions are **strictly one-way** and can only originate from the `OPEN` state. 

Valid transitions:
*   `OPEN` -> `ESCALATED`
*   `OPEN` -> `CLEARED`
*   `OPEN` -> `CONFIRMED_HIT`

**Rule Enforcement:** The Service layer is the absolute authority on state. If a client attempts to mutate an alert that is not in the `OPEN` state, the Service must return a domain-level error (e.g., `ErrAlertDecided`), which the API layer translates to an HTTP `409 Conflict`.

---

## 4. Technical Implementation Strategies

### A. Endpoint Validation & Payload Multi-Tenancy (API Layer)
To accelerate development, `tenantId` will be passed directly in the request JSON body (for POST/PATCH) or as a query parameter (for GET). 
We will use a validation library (e.g., `go-playground/validator/v10`) bound to Data Transfer Objects (DTOs) to enforce schema correctness before requests hit the business logic.

*Pseudo-structure for DTO:*
```text
DecisionRequestDTO:
  TenantID     (String, Required)
  Status       (String, Required, Must be CLEARED or CONFIRMED_HIT)
  DecisionNote (String, Required)
```
If validation fails, the API immediately returns `400 Bad Request`. The Service layer assumes all input data is structurally sound but enforces business logic.

### B. In-Memory Thread Safety (Storage Layer)
Because HTTP requests are handled concurrently, the in-memory storage must be thread-safe.
*   **Pattern:** A native map (`map[string]*Alert`) wrapped inside a struct alongside a `sync.RWMutex`.
*   **Behavior:** Use read locks (`RLock`) for queries and full locks (`Lock`) for creation and mutation. This provides compile-time type safety while preventing concurrent read/write panics.

### C. Event Publishing
Defined as an interface in the domain package. When an alert is escalated or decided, an event payload is constructed and passed to the publisher. For this MVP, the implementation will serialize the event to JSON and write it to standard output (`stdout`).

---

## 5. Engineer Task Plan (Execution Steps)

The engineer must implement the service layer-by-layer:

### Task 1: Domain Definition
*   Create `internal/domain`.
*   Define the `Alert` model, status constants, and domain event structures.
*   Define `AlertRepository` interface (`Save`, `FindByID`, `FindAll`).
*   Define `EventPublisher` interface (`Publish`).
*   Define standard domain errors (e.g., `ErrNotFound`, `ErrImmutableState`, `ErrTenantMismatch`).

### Task 2: Storage Layer (In-Memory)
*   Create `internal/storage`.
*   Implement `AlertRepository` using the map + `sync.RWMutex` pattern.
*   Ensure methods clone data or handle pointers carefully to prevent accidental mutation of stored data outside the mutex lock.

### Task 3: Service Layer (Business Logic)
*   Create `internal/service`.
*   Inject the Repository and Event Publisher interfaces.
*   Implement `CreateAlert`, `ListAlerts`, `DecideAlert`, `EscalateAlert`.
*   Enforce the State Machine rules and Tenant Isolation.
*   **Testing:** Write comprehensive unit tests for the Service layer covering all valid/invalid state transitions and tenant mismatch scenarios.

### Task 4: API Layer & Validation
*   Create `internal/api`.
*   Define Request/Response DTOs and configure the validator.
*   Implement standard `net/http` handlers using the native routing capabilities (path parameters).
*   Map domain errors to accurate HTTP status codes (400, 404, 409).

### Task 5: Application Wiring
*   Create `cmd/server/main.go`.
*   Instantiate the Storage, Publisher, and Service.
*   Bind the API handlers to the HTTP server.
*   Implement graceful shutdown.

---

## 6. Architectural Decisions, Assumptions & Future Improvements

### Key Decisions
*   **Native Go Router:** Utilizing the standard `net/http` multiplexer (enhanced in Go 1.22+) to avoid bloated third-party web frameworks for a simple API.
*   **Explicit Payload Data:** `tenantId` is included in the request body/query rather than headers. This simplifies the MVP client interaction and testing.
*   **Mutex-Protected Map:** Chosen over `sync.Map` because it provides strict type safety and is highly idiomatic for general-purpose in-memory data stores.
*   **Layered over Feature-Based Directory Structure:** For an MVP microservice focused solely on "Alerts", a layered structure (`domain`, `service`, `api`) prevents Go package naming collisions (stuttering) and is simpler to navigate. We will defer "Package-by-Feature" vertical slicing until the domain expands significantly.

### Assumptions
*   **Authentication/Authorization:** Assumed to be handled upstream (e.g., via an API Gateway) for this MVP. The service trusts the `tenantId` provided in the payload.
*   **Event Volume:** The volume of events is low enough that blocking to write to `stdout` will not bottleneck the HTTP response time.

### Future Improvements (Post-MVP)
*   **Persistent Storage:** Swap the in-memory repository with a PostgreSQL implementation using standard SQL or a lightweight ORM (e.g., `sqlx`, `pgx`). The Clean Architecture ensures the Service layer won't change.
*   **Message Broker Integration:** Swap the `stdout` Event Publisher with an implementation that writes to Kafka, RabbitMQ, or AWS SNS/SQS.
*   **Tenant Extraction Middleware:** Move `tenantId` extraction to an HTTP middleware that reads from a secure JWT or Header, placing it into the request Context (`context.Context`), removing the need for clients to pass it in the body.
*   **Structured Logging:** Implement `log/slog` for standardized JSON logging across the application.
*   **Concurrency Control (ETags/Optimistic Locking):** As traffic scales, implement versioning on the Alert entity to prevent race conditions if two analysts attempt to decide the same alert at the exact same millisecond.