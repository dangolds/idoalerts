# Fincom — Backend Home Assignment

## Sanctions Alert Service

### Context

You're joining a financial compliance platform that screens payment transactions against sanctions lists. When a transaction matches a sanctioned entity, an **alert** is created for a compliance officer to review. Your task is to build a small **Alert Management Service**.

---

### Requirements

Build a REST API service (in **Go preferred**) that manages screening alerts with the following:

#### Domain Model

- **Alert**: Represents a potential sanctions match on a transaction
  - `id` (UUID)
  - `transactionId` (string)
  - `matchedEntityName` (string — the sanctioned entity that was matched)
  - `matchScore` (float, 0–100 — how confident the match is)
  - `status` (enum: `OPEN`, `ESCALATED`, `CLEARED`, `CONFIRMED_HIT`)
  - `assignedTo` (string, nullable — analyst user ID)
  - `tenantId` (string — multi-tenant isolation)
  - `createdAt`, `updatedAt` (timestamps)
  - `decisionNote` (string, nullable — analyst's reasoning)

#### API Endpoints

1. **`POST /alerts`** — Create a new alert (simulating an incoming screening result)
2. **`GET /alerts?tenantId=X&status=Y&minScore=Z`** — List alerts with filtering
3. **`PATCH /alerts/{id}/decision`** — Submit a decision (`CLEARED` or `CONFIRMED_HIT`) with a `decisionNote`. This is **immutable** — once decided, it cannot be changed.
4. **`POST /alerts/{id}/escalate`** — Escalate an alert (changes status to `ESCALATED`, must publish an event)

#### Event Publishing (Simulated)

When an alert is **escalated** or **decided**, publish an event to stdout (simulating a message broker). The event should be a structured JSON log line, e.g.:

```json
{"event": "alert.decided", "alertId": "...", "tenantId": "...", "decision": "CLEARED", "timestamp": "..."}
```

#### Constraints & Rules

- Alerts must be **tenant-isolated** — a query without `tenantId` must return `400 Bad Request`
- Decision is **write-once** — attempting to decide an already-decided alert returns `409 Conflict`
- An alert can only be escalated if its current status is `OPEN`
- Use **in-memory storage** (no database required) — but structure the code as if a real DB will replace it later (repository pattern)

---

### What We're Looking For

| Area | What we'll evaluate |
|------|---------------------|
| **Code structure** | Clean separation of concerns (handler → service → repository) |
| **Domain logic** | Correct state machine transitions, immutability of decisions |
| **API design** | Proper HTTP status codes, error responses, input validation |
| **Multi-tenancy** | Tenant isolation enforced at the right layer |
| **Event-driven thinking** | How you model and emit domain events |
| **Testability** | At least 3–4 unit tests covering key business rules (decision immutability, tenant isolation, invalid transitions) |
| **Code quality** | Naming, error handling, idiomatic patterns |

