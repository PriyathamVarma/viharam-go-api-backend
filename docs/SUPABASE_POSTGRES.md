# Supabase PostgreSQL backend

Supabase is the selected persistence platform for Viharam. The Go/Gin API connects to the Supabase PostgreSQL database using `pgx`; Supabase is not accessed as a Mongo-compatible document store.

```text
Web and mobile clients -> Go/Gin -> pgx -> Supabase PostgreSQL
                                          ├─ Auth (optional identity provider)
                                          ├─ Storage (optional file provider)
                                          └─ Realtime (optional client updates)
```

## Configuration

Use the Supabase transaction-pooler connection string in `SUPABASE_DB_URL`:

```text
SUPABASE_DB_URL=postgresql://postgres.<project-ref>:<password>@<pooler-host>:6543/postgres?sslmode=require
SUPABASE_URL=https://<project-ref>.supabase.co
SUPABASE_SERVICE_ROLE_KEY=<server-only-key>
```

`SUPABASE_SERVICE_ROLE_KEY` must never be sent to the browser or mobile application. The Go API should use the database connection for transactional domain operations. Supabase REST calls are only needed for services that are not represented by PostgreSQL access, such as Storage or selected Auth operations.

## Persistence rules

- Use UUID primary keys and foreign keys for domain relationships.
- Store money as integer INR paise (`bigint`), never floating-point rupees.
- Use PostgreSQL transactions for booking, slot, lock, cancellation, and refund workflows.
- Use row-level locking (`SELECT ... FOR UPDATE`) when reserving capacity.
- Use database constraints for uniqueness, valid status values, and non-negative inventory.
- Keep the public API response envelopes unchanged while replacing Mongo repositories with PostgreSQL repositories.
- Keep Supabase RLS disabled for the private server connection only if the Go API is the sole trusted data boundary; enforce authorization in Go and use database roles carefully.

## Migration status

The Go repository already contains a PostgreSQL pool and an initial core schema. Authentication, catalogue, properties, property bookings, experience slots, core experience bookings, host profiles, support tickets, incidents, experience reviews, property reviews, coupons, referrals, and loyalty now use PostgreSQL. Flexible domains use a PostgreSQL JSONB document table while their stable schemas are being promoted. The former Appwrite adapter has been removed. MongoDB is only a temporary fallback when PostgreSQL is not configured; PostgreSQL wins if both are present.

Before the Mongo compatibility path is removed, the remaining domain tables and PostgreSQL repositories must cover users, host profiles, experiences, properties, availability, bookings, reviews, marketplace, support, safety, governance, trips, gifts, loyalty, referrals, coupons, and pricing rules.
