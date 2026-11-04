# Viharam API migration status

## Scope

The Next.js application currently exposes the route files under `/api/v1`; the checked-in parity verifier currently discovers 156 HTTP handlers. The Go/Gin service is the target runtime for all of these handlers.

The existing Next.js API uses MongoDB/Mongoose for transactional data. The Go migration therefore preserves MongoDB collection names, document identifiers, role semantics, response envelopes, cookies, and workflow rules. PostgreSQL-only benchmark tables are not the source of truth for the application migration.

## Migration rules

- Preserve the public path and HTTP method exactly.
- Preserve the existing success and failure JSON envelope.
- Preserve the `token` httpOnly cookie contract for browser and mobile clients.
- Use Argon2id for new passwords; accept legacy hashes during migration where required.
- Enforce the same role and ownership rules in Go middleware and services.
- Use PostgreSQL transactions and row locks for multi-record booking and inventory workflows.
- Keep payment, email, storage, and search integrations behind explicit service interfaces.
- Do not register an endpoint until its validation, authorization, persistence, and response contract are implemented.

## Domain slices

1. Runtime and Supabase PostgreSQL platform: configuration, connection pooling, indexes, transactions, errors, health, and request tracing.
2. Authentication and users: register, login, demo login, logout, current user, legacy password migration, and role guards.
3. Public catalogue: search, categories, experiences, creator profiles, properties, and marketplace products.
4. Availability and bookings: slots, locks, property bookings, experience bookings, cancellations, refunds, and pricing.
5. Host and ambassador operations: onboarding, KYC, documents, assignments, checklists, proof, escalation, and feedback.
6. Trust and safety: traveller safety, incidents, disputes, reviews, reports, helpful votes, and audit logs.
7. Commerce and engagement: coupons, referrals, loyalty, gifts, group trips, itineraries, cart, support tickets, and email jobs.

## Current implementation

The Go service currently contains the platform foundation, Supabase-compatible PostgreSQL pooling, core/property/booking schema migrations, PostgreSQL authentication, catalogue, property, property-booking, experience-slot, experience-booking, booking-lock, rescheduling, host onboarding, support-ticket, incident, experience-review, property-review, coupon, referral, and loyalty handlers, plus a PostgreSQL JSONB document layer for the remaining flexible domains. Host profiles now use the relational `host_profiles` table, including onboarding state, KYC, address, bank-detail masking, documents, agreement, safety checklist, and verification actions. The route-parity verifier now inspects the Supabase runtime branch (not the legacy Mongo branch) and reports all current 156 Next.js handlers covered by 165 Go registrations. The web app now forwards `/api/v1/*` traffic to `GO_API_URL` through a `beforeFiles` rewrite. The document-backed domains still need semantic contract tests and promotion of stable fields into dedicated tables. MongoDB is now selected only when PostgreSQL is absent, as a temporary local compatibility mode. See `docs/SUPABASE_POSTGRES.md`.

## Completion criteria

The migration is complete only when:

- Every currently discovered handler has a Go route registration.
- Every route has contract tests for success, validation failure, unauthorized access, forbidden access, missing resources, and database failure where applicable.
- Web and mobile clients can use the Go base URL without Next.js API fallbacks.
- Supabase PostgreSQL constraints, indexes, and transaction paths are verified against the migrated schema.
- A route inventory test proves there are no unimplemented or shadowed paths.
