# Viharam API migration plan

## Compatibility contract

The Go service will retain the existing `/api/v1` prefix and the current role names so that the web and mobile clients can be migrated by changing their API base URL rather than rewriting every screen.

The response envelope remains compatible with the current application:

```json
{
  "success": true,
  "data": {},
  "message": "..."
}
```

## Module migration order

1. `health` and platform middleware
2. `auth` and identity
3. `experiences`, categories, and search
4. `experience-slots` and booking locks
5. `experience-bookings`, payment intents, and webhooks
6. `properties` and property bookings
7. `host-profiles` and KYC
8. `ambassadors` and assignment workflows
9. reviews and moderation
10. support tickets and traveller safety
11. group trips, chat, and itineraries
12. marketplace, gifts, referrals, and loyalty
13. admin governance, incidents, disputes, cancellations, refunds, and reports

Each module should be migrated with:

- model/input/output types
- repository queries
- service-level business rules
- Gin handlers
- role middleware
- migration SQL
- OpenAPI paths
- unit tests and handler contract tests

## PostgreSQL schema mapping

MongoDB collections become PostgreSQL tables. Flexible MongoDB subdocuments such as host KYC, address, safety checklists, experience content, and status history remain `jsonb` initially to preserve the current shape while transactional entities use relational columns and foreign keys.

Money is represented as integer paise. Dates and times use PostgreSQL `date`, `time`, and `timestamptz`. Public IDs are UUIDs. The schema deliberately uses explicit status checks and indexes for traveller, host, ambassador, and admin queries.

## Cutover strategy

1. Run the Go API beside the existing Next.js API.
2. Add contract tests against both implementations.
3. Migrate read-only catalogue endpoints first.
4. Move authentication and booking reads.
5. Move booking writes only after payment and transaction tests pass.
6. Switch the web and mobile clients through `GO_API_BASE_URL` / `EXPO_PUBLIC_API_URL`.
7. Remove Mongoose models and Next.js API routes only after all clients and background workflows use Go.
