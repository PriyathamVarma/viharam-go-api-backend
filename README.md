# Viharam Go/Gin API

Standalone backend for the Viharam web and mobile applications.

This is Viharam's standalone Go/Gin API backend for the travel marketplace domain.

## Architecture

```text
cmd/api                     Process bootstrap and graceful shutdown
internal/app                Gin application and route composition
internal/config             Environment parsing and validation
internal/middleware         Request ID, recovery, logging, CORS, auth, RBAC
internal/platform/database  PostgreSQL pool and transaction helpers
internal/platform/httpx     JSON envelopes, errors, pagination
internal/modules             Domain modules
  auth                       Registration, login, current user, logout
  experiences                Catalogue and search
  bookings                  Availability locks and experience bookings
  properties                Homestays and property bookings
  hosts                     Host onboarding and KYC
  ambassadors               Ambassador profiles and assignments
  support                   Support tickets
  marketplace               Products, carts, orders, gifts, loyalty
  trips                     Group trips and itineraries
  reviews                   Reviews and moderation
  admin                     Governance, audit, incidents, disputes, refunds
db/migrations               Reviewed PostgreSQL schema migrations
openapi                     API contract (added incrementally with modules)
```

The backend owns all application data. The Next.js app becomes a frontend/server-rendered client and should stop importing Mongoose models or handling business mutations.

Supabase PostgreSQL is the target persistence platform. MongoDB is retained only as a compatibility adapter for handlers that have not yet been rewritten. See `docs/SUPABASE_POSTGRES.md` for connection, transaction, and migration rules.

## Viharam domain principles

- The selected persistence provider is Supabase PostgreSQL through `pgx`; MongoDB remains available only during migration compatibility work.
- Money is stored as integer INR paise, never floating-point rupees.
- UUIDs are used for public identifiers.
- Booking and inventory-like capacity changes use PostgreSQL transactions, row locks, constraints, and idempotency keys.
- Every mutation is scoped to the authenticated user, host, ambassador, or administrator role.
- Public catalogue reads are separated from authenticated mutations.
- Audit records are append-only.
- Search and pagination are database-backed; mock data is not used by the API.
- The API keeps `/api/v1` and Viharam role names for client compatibility.

## Run locally

```bash
cp .env.example .env
go mod download
go run ./cmd/api
```

Apply migrations using the project migration command once it is added to the deployment pipeline. The first migration is intentionally reviewable SQL rather than an ORM-generated schema.

## Health endpoints

- `GET /health` — process liveness
- `GET /ready` — PostgreSQL readiness
- `GET /version` — build metadata

## Migration status

The current route inventory is documented in `docs/API_MIGRATION_STATUS.md`. Remaining domain modules should be migrated one at a time behind the existing Next.js contract, with contract tests before switching the frontend base URL.
