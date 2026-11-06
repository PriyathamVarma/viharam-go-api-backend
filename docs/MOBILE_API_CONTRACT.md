# Mobile API contract

What the Viharam mobile app (`viharam-mobile-app/src/api/*`) can rely on from the Go API in
**PostgreSQL (Supabase) mode**, which is the authoritative runtime. The MongoDB compatibility
branch (`MONGODB_URI` without `SUPABASE_DB_URL`) serves the older routes only: the new portal
endpoints below (`/host/*`, `/ambassador/*`, `/guests`) are not registered there; `POST /uploads` is.

Everything here was read from the handlers in `internal/modules/*`. Nothing was run against a live
database, so SQL-backed shapes are verified by reading, not by execution (see "Verification").

## Conventions

* Base path `/api/v1`. Auth: `Authorization: Bearer <jwt>` (or the `token` cookie). Roles in the JWT
  are `Traveller`, `Host`, `Ambassador`, `Admin`, `SuperAdmin`.
* Success: `{ "success": true, "data": <payload>, "message": "<optional>" }` (HTTP 200, or 201 on create).
* Failure: `{ "success": false, "message": "...", "error": { "code": "...", "message": "..." } }`.
  Codes in use: `unauthorized` 401, `forbidden` 403, `not_found` 404, `invalid_request` 400,
  `invalid_id` 400 (malformed UUID), `conflict` 409, `database_error` 500, `file_too_large` 413,
  `unsupported_media_type` 415.
* Money in the new portal endpoints is **rupees** (number, 2 decimals). Storage is integer paise.
* "JSONB document" endpoints (documentpg) store the request body as-is, add `_id`, `createdAt`,
  `updatedAt`, and return lists as `{ "<key>": [...], "total": n, "page": 1, "limit": 20 }`
  (`?page=`, `?limit=` max 100; equality filters `status, category, city, state, experienceId,
  userId, creatorType` match `payload.<key>`). Owner-scoped domains set `userId` (or the owner
  field) from the token and only return the caller's rows; Admin/SuperAdmin see all.

---

## New endpoints

### GET /host/listings  (Host)

```json
{ "success": true, "data": {
  "total": 3,
  "listings": [
    { "id": "uuid", "_id": "uuid", "kind": "property", "title": "Konaseema Riverside Homestay",
      "subtitle": "homestay", "status": "live", "rawStatus": "active", "price": 2500, "priceUnit": "night",
      "city": "Amalapuram", "photo": "https://.../a.jpg", "bookingCount": 12, "createdAt": "2026-10-01 10:00:00+00" },
    { "id": "uuid", "kind": "experience", "title": "Coconut grove walk", "subtitle": "Farm & Agriculture · 2 hours",
      "status": "live", "rawStatus": "published", "price": 600, "priceUnit": "person", "city": "Amalapuram",
      "photo": "", "bookingCount": 4 },
    { "id": "uuid", "kind": "creator_experience", "title": "Palm-leaf weaving", "subtitle": "Crafts · 2 hours",
      "status": "in_review", "rawStatus": "", "price": 800, "priceUnit": "person", "city": "Amalapuram",
      "photo": "", "bookingCount": 0 } ] } }
```

* `kind`: `property` (table `properties`), `experience` (table `experiences`), `creator_experience`
  (what `POST /creator-experiences` stores; waiting for review/promotion). Order: properties,
  experiences, creator experiences, each most-recently-updated first.
* `status`: `live | in_review | draft | paused | rejected | archived`. `rawStatus` is the stored value
  (creator experiences usually have none, which maps to `in_review`).
* `photo`: property `image`, else first of `images`; experiences use `content.image|photo|coverImage|
  heroImage|thumbnail|images|photos|gallery`; creator experiences the same keys in the document. `""` when none.
* `bookingCount`: bookings with status `confirmed` or `completed` (always 0 for creator experiences).
* Errors: 401, 403 (not a Host).

### GET /host/earnings  (Host)

Derived from the host's `confirmed` / `completed` bookings: property bookings (`total_price_paise`,
host `property_bookings.host_id`) and experience bookings (`subtotal_paise`, the traveller service fee
is excluded) on experiences the host owns. Platform commission is **15%** (half-up per booking).
Month bucketing uses the stay end date (check-out / experience date) in IST; a booking without one falls back to its completion or creation time, and missing amounts count as 0.

```json
{ "success": true, "data": {
  "currency": "INR", "commissionRate": 0.15, "bookingCount": 5,
  "totals":    { "gross": 21000, "platformCommission": 3150, "net": 17850 },
  "thisMonth": { "gross": 8000,  "platformCommission": 1200, "net": 6800 },
  "lastMonth": { "gross": 12000, "platformCommission": 1800, "net": 10200 },
  "pendingPayout": 7650, "pendingBookingCount": 3, "nextPayoutDate": "2026-11-16",
  "monthly": [ { "month": "2026-06", "label": "Jun", "gross": 1000, "platformCommission": 150, "net": 850 } ],
  "payoutHistory": [ { "id": "payout-2026-10-12", "date": "2026-10-12", "amount": 10200, "bookingCount": 2, "status": "paid" } ],
  "bankLinked": true, "payoutsSource": "derived" } }
```

* `monthly` always has 6 entries, oldest first, ending with the current month.
* **There is no payouts table**, so payouts are derived (`payoutsSource: "derived"`): a stay becomes
  payable on the first Monday on/after `stayEnd + 7 days`. `completed` bookings past that date are
  `paid` and grouped by payout date into `payoutHistory` (net amount, newest first, max 20);
  everything else (`confirmed`, or not yet due) is `pendingPayout`. `nextPayoutDate` is the earliest
  future payout date among pending items, else the next Monday.
* `bankLinked`: host profile `bankDetails.accountNumber` is non-empty.
* Replace the derivation with a ledger query when a payouts table exists.

### GET /ambassador/earnings  (Ambassador)

From the caller's `ambassadorassignments` documents with `status = "completed"` (matched by
`owner_id` or `payload.ambassadorId` = user id). Share per assignment is `ambassadorShare` (rupees),
falling back to `share`; an assignment without either (or with a negative/non-numeric value) contributes 0 but still counts as an experience.
Completion time: `completedAt`, else `experienceDate`, else the record's `updatedAt` (unparsable values fall through to the next). Ratings outside 1-5 are ignored.

```json
{ "success": true, "data": {
  "currency": "INR", "totalEarned": 1600, "thisMonth": 480, "experiencesCount": 4, "avgRating": 4.5,
  "monthly": [ { "month": "2026-11", "label": "Nov", "amount": 480 } ],
  "pendingPayout": 1200, "nextPayoutDate": "2026-11-21", "bankLinked": false, "payoutsSource": "derived" } }
```

* `monthly`: 6 entries, oldest first. Payouts are derived: paid on the 21st of the month after
  completion; `pendingPayout` is everything whose payout date is still in the future.
* `avgRating`: mean of `guestFeedback.rating` over completed assignments, else the ambassador profile's
  `avgGuestRating`, else 0. `bankLinked`: profile `bankLinked|bankAccountLinked|bankAccountConnected`
  is true or `bankDetails.accountNumber` is set.

### GET /ambassador-assignments/:id/guests  (assigned Ambassador, or Admin)

```json
{ "success": true, "data": {
  "assignmentId": "uuid", "bookingId": "uuid", "bookingRef": "VHM-AB12CD34EF", "partySize": 4,
  "guests": [ { "name": "Asha Rao", "partySize": 4, "dietaryNotes": "We are vegetarian; Allergies: peanuts",
                "specialRequests": "We are vegetarian", "status": "confirmed" } ] } }
```

* One entry per booking (the platform stores a lead traveller and a party size, not individual guests).
  `dietaryNotes` = the special requests when they mention food/allergies, joined with the traveller's
  safety-profile `allergies`. Phone, email and medical details are deliberately not returned.
* 403 for other ambassadors and for `rejected`/`cancelled` assignments; 404 unknown assignment;
  400 malformed id. An unlinked booking returns `guests: []`.

### GET /ambassador/hosts  (Ambassador)

```json
{ "success": true, "data": { "total": 1, "hosts": [
  { "id": "uuid", "_id": "uuid", "userId": "uuid", "name": "Riverside Homestay family", "hostType": "Homestay",
    "village": "Amalapuram", "district": "Konaseema", "state": "Andhra Pradesh", "listingsCount": 2,
    "verificationStatus": "verified", "lastVisited": "2026-10-14" } ] } }
```

* Region = the ambassador profile's `cities`/`city` matched against the host address `village` or
  `district` (or an experience `city`); with no cities, the profile's `states`/`state` against the host
  `state`; plus any host whose experience the ambassador has been assigned to. Max 100, by name.
* `listingsCount`: non-archived experiences + properties. `lastVisited`: latest `experienceDate` of the
  ambassador's `completed` assignments on that host's experiences, else `null`. Completed `ambassador_visits` (see below) also count: `lastVisited` is the later of the two dates.

### POST /ambassador-assignments/:id/message-guests  (assigned Ambassador, or Admin)

Body `{ "message": "string, 1..1000 characters (trimmed)" }` -> `data: { "sent": 1, "messageId": "uuid" }`, message "Message sent".

* Stored in table `assignment_messages` (`id, assignment_id, sender_user_id, body, created_at`).
* The repo has **no notifications table or push channel**, so nothing is pushed. `sent` is the number of
  distinct travellers on the assignment's booking (0 if the booking is missing), i.e. the guests who can be shown the message.
* Errors: 400 empty/too long/missing message; 401; 403 other ambassadors/roles; 404 unknown assignment; 400 `invalid_id`;
  409 `conflict` for `rejected`/`cancelled` assignments. Order of checks: 404, 403, 400, 409.

### GET /ambassador-assignments/:id/messages  (same auth)

`data: { "messages": [ { "id": "uuid", "body": "...", "createdAt": "2026-11-06T10:00:00Z" } ] }`, newest first, max 200.
Not blocked for rejected/cancelled assignments.

### Ambassador visits  (Ambassador only; table `ambassador_visits`)

* `POST /ambassador/visits` body `{ hostProfileId (uuid of host_profiles.id, as returned by GET /ambassador/hosts `id`),
  scheduledFor: "YYYY-MM-DD" (today or later, IST), time?: "HH:mm", notes?: string <= 500 }` -> **201**
  `data: { id, hostProfileId, scheduledFor, time ("" if none), notes, status: "scheduled" }`.
  400 `invalid_request` for a bad id/date/past date/time/notes, 404 if the host profile does not exist.
* `GET /ambassador/visits` -> `data: { visits: [ { id, hostProfileId, hostName, village, scheduledFor, time, notes, status } ] }`,
  the caller's own, ascending by date then time (no time last). `status`: `scheduled | completed | cancelled`.
* `PATCH /ambassador/visits/:id` body `{ status: "completed" | "cancelled" }` (owner only) -> same object as the POST response
  with the new status. 400 bad status/id, 403 not the owner, 404 unknown, 409 `invalid_state` when the visit is already
  completed/cancelled and a *different* status is requested (the same status again is a no-op success).

### POST /uploads  (any authenticated user)

`multipart/form-data`, field `file` (also accepted: `image`, `photo`, `document`).

```json
{ "success": true, "message": "File uploaded", "data": {
  "url": "https://api.example.com/uploads/3f2b...e1.jpg", "path": "/uploads/3f2b...e1.jpg",
  "name": "3f2b...e1.jpg", "mimeType": "image/jpeg", "size": 183422 } }
```

* Allowed (detected from content, not the filename or header): JPEG, PNG, WebP, PDF. Max
  `MAX_UPLOAD_BYTES` (default 10 MB). Errors: 400 no file, 413 `file_too_large`, 415 `unsupported_media_type`.
* Stored as `<UPLOAD_DIR>/<uuid><ext>` (default `./uploads`), served read-only at `GET /uploads/<name>`
  with `nosniff`, a locked-down CSP and no directory listing. `url` uses `PUBLIC_BASE_URL` if set, else
  the request scheme (`X-Forwarded-Proto`) and host. Use `url` as the document/proof/photo URL in
  other calls (`documents`, `proof`, `feedback.proofUrl`, ...).
* Files are public to anyone holding the (unguessable) URL, including KYC documents. Local disk is
  ephemeral on hosts without a volume (Render): mount a disk at `UPLOAD_DIR` before relying on it.

---

## Existing endpoints used by the app

### Host profiles (`hostprofilespg`, table `host_profiles`)

Profile object (`data` of create/get/patch/action; bank account number is masked `****1234` for
non-admins):
`{ _id, userId, displayName, hostType, description, phone, address{}, kyc{}, bankDetails{}, documents[],
safetyChecklist[], agreementAccepted, verificationStatus, onboardingStep, onboardingCompleted, altPhone?,
website?, agreementVersion?, agreementAcceptedAt?, createdAt, updatedAt }`.
`verificationStatus`: `pending | under_review | verified | suspended | rejected`.

| Call | Request body | Response |
|---|---|---|
| `GET /host-profiles` | – | `data: [profile]` (a bare array; the caller's own, or all for admin; 403 if not Host) |
| `POST /host-profiles` | `{displayName, hostType, description?, phone, altPhone?, website?}` (first three required) | 201 `data: profile`; 409 `conflict` if one exists |
| `GET /host-profiles/:id` | – | `data: profile` (403 if not owner) |
| `PATCH /host-profiles/:id` | any of `displayName, hostType, description, phone` (other keys, incl. `altPhone`/`website`, are ignored) | `data: profile` |
| `PATCH /:id/kyc` | `{fullLegalName, dateOfBirth, idType, idNumber}` stored verbatim | `data: profile` (`onboardingStep` >= 2) |
| `PATCH /:id/address` | `{line1, line2?, village?, tehsil?, district, state, pincode}` stored verbatim | `data: profile` (step >= 3) |
| `PATCH /:id/bank-details` | `{accountHolderName, accountNumber, ifscCode, bankName, branchName?}` stored verbatim | `data: profile` (masked; step >= 4) |
| `PATCH /:id/safety-checklist` | stored verbatim as the **whole** checklist | `data: profile` (step >= 5) |
| `POST /:id/agreement` | `{version?}` (default `v1.0`) | `data: profile` with `agreementAccepted: true` |
| `GET /:id/documents` | – | `data: [doc]` |
| `POST /:id/documents` | `{type, label, url, mimeType?}` (type, label, url required) | 201 `data: [all docs]`, doc = `{_id, type, label, url, mimeType, status:"pending", uploadedAt}` |
| `POST /:id/submit-for-review` | none | `data: profile` with `verificationStatus:"under_review"`, `onboardingCompleted:true`, step 6; 400 `incomplete_onboarding` unless kyc, address and bankDetails are non-empty, >= 2 documents, agreement accepted (the list of missing parts is not returned) |
| `PATCH /:id/status`, `PATCH /:id/documents/:docId` | admin only | – |

Validation and writes are unvalidated beyond the above (`idType`, `hostType`, document `type` are free text).

### Experience slots (`experiencebookingspg`)

* `GET /experience-slots?experienceId=<uuid>&month=YYYY-MM` (public). `experienceId` must be a UUID of a row in
  `experiences` (400 otherwise). `month` is optional and filters server-side to that calendar month (400
  `invalid_request` if malformed); without it all slots of the experience are returned.
  `data` is `{ slots: [...], total }` (the shape the web app and `host.ts` read; `traveller.ts` accepts it too):
  `[{ _id, experienceId, date:"2026-11-14", startTime:"09:00:00", endTime:"11:00:00", capacity, bookedCount,
  lockedCount, isBlocked, availableSpots }]`, ordered by date, start time. Times are Postgres `time::text`
  (`HH:MM:SS`). **Change:** this used to be a bare array.
* `POST /experience-slots` (Host/Admin): `{experienceId, date:"YYYY-MM-DD", startTime:"HH:MM[:SS]",
  endTime:"HH:MM[:SS]", capacity>=1, isBlocked?}` -> `data: {_id}`, message "Experience slot saved".
  Not an upsert: a duplicate (experience, date, startTime) returns 409 `slot_error`. `priceOverride` is
  ignored. There is no check that the host owns the experience.
* `PATCH /experience-slots/:id`: same body (all fields optional but `capacity` must be sent) -> `data:{_id}`.
  `DELETE /experience-slots/:id` -> 200, no data.

### Pricing rules (`documentpg`, domain `pricingrules`, owner `userId`)

* `GET /pricing-rules?experienceId=` -> `data: { rules: [...], total, page, limit }` (the caller's rules only).
* `POST /pricing-rules` body is stored verbatim (the app sends `{experienceId, name, type, adjustmentType,
  adjustmentValue, startDate?, endDate?, minGuests?, maxAgeForChild?}`) -> 201 `data: {…body, userId, _id, createdAt, updatedAt}`.
  No server-side validation in PostgreSQL mode.
* `PATCH /pricing-rules/:id` (owner, merges), `DELETE /pricing-rules/:id` (owner; `message: "Record deleted"`).
* `POST /pricing-rules/calculate` is public but only **stores** the body as a record; it does not compute prices.

### Blackout dates (`documentpg`, domain `blackoutdates`)

* `GET /blackout-dates?experienceId=` -> `data: { dates: [{_id, experienceId, date, reason?, userId, createdAt, updatedAt}], total, page, limit }`.
* `POST /blackout-dates` `{experienceId, date:"YYYY-MM-DD", reason?}` (verbatim) -> 201 record. No uniqueness or format check.
* `DELETE /blackout-dates/:id` (owner) -> 200 `message: "Record deleted"`.

### Creator experiences (`documentpg`, domain `creatorexperiences`)

* `POST /creator-experiences` body verbatim (the app sends `{title, category, location, description,
  pricePerPerson, duration, maxGroupSize, languages[]}`) -> 201 `data: {…body, userId, _id, createdAt, updatedAt}`.
  No `status` is added (these show as `in_review` in `/host/listings`).
* `GET /creator-experiences` -> `data: { experiences: [...], total, page, limit }` (own). `GET/PATCH/DELETE /:id` owner-scoped.
* These documents are not rows in `experiences`, so slots/pricing cannot be created against them
  until promoted (slot `experienceId` must be an `experiences` UUID).

### Ambassador assignments (`documentpg`, domain `ambassadorassignments`, owner `ambassadorId`)

* `GET /ambassador-assignments` (Ambassador/Admin) -> `data: { assignments: [...], total, page, limit }`, the
  caller's own. Documents are free-form; the app reads `_id, bookingId, bookingRef, experienceTitle|title,
  experienceDate|date, startTime, guests|guestCount, city|location, meetingPoint, status, ambassadorShare|share,
  matchReasons[], proof|proofs`. `status`: `pending_acceptance | accepted | rejected | active | completed | escalated | cancelled`.
* `GET /ambassador-assignments/:id` -> the document (403 unless `payload.ambassadorId` is the caller or admin).
* `POST /:id/accept`, `/reject`, `/proof`, `/feedback` are an assignment state machine (404 unknown id, 400 malformed id,
  403 not the assigned ambassador, 409 `invalid_state` when the action does not fit the current status). The assigned
  ambassador is the record owner or `ambassadorId`; admins may reject, upload proof and report on their behalf, but
  only the assigned ambassador can accept. A missing status counts as `pending_acceptance`. Response is unchanged:
  `data: <whole document>`, `message: "Action completed"`.

  | Action | Body | Allowed from | Result |
  |---|---|---|---|
  | `accept` | `{}` | `pending_acceptance` | `status: "accepted"`, `acceptedAt`, `accept: body` |
  | `reject` | `{reason?}` | `pending_acceptance`, `accepted` | `status: "rejected"`, `rejectedAt`, `reject: body` |
  | `proof` | `{type?, url, caption?}` or `{urls: []}` | `accepted` (-> `in_progress`), `in_progress`, `active` | `proofs: [...]` appended (`{type, url, caption, uploadedAt}`), `proof` = the last item; 400 without a URL |
  | `feedback` | `{notes, issue, proofUrl?, rating?}` | `accepted`, `in_progress`, `active` | `status: "completed"`, `completedAt`, `feedback: body`, and `guestFeedback: {rating, notes, submittedAt}` when `rating` is 1-5 |

  `proofs` is an array, so the app's `proofCount` now counts uploads. `GET /ambassador/earnings` counts an assignment once it
  is `completed`. `PATCH /:id/checklist` and `POST|PATCH /:id/escalate` stay generic: the body is stored under the action
  name, `status` is not changed, owner or admin only.
* `POST /ambassador-assignments/auto-assign` and `/override` are admin tooling.

### Ambassadors

* `GET /ambassadors` and `GET /ambassadors/:id` are **public** (no token): `data: { ambassadors: [...], total, page, limit }`
  / the profile document (`userId, name, city|cities[], isActive, isVerified, performanceScore, completedAssignments,
  totalAssignments, avgGuestRating, bio, languages[], specialties[], unavailableDates[], maxAssignmentsPerDay, createdAt`).
  The app picks the profile whose `userId` equals the signed-in user.
* `GET /ambassadors/:id/availability` (auth) returns the whole profile document.
* `PATCH /ambassadors/:id/availability` `{unavailableDates[], blockedDates[], maxAssignmentsPerDay}` (all optional, at least one;
  the ambassador whose profile it is, or an admin, else 403). Dates must be `YYYY-MM-DD` (max 400), `maxAssignmentsPerDay` a
  whole number 1-20 (400 otherwise). `unavailableDates` and `blockedDates` are merged, de-duplicated, sorted and stored under
  **both** top-level keys, plus the normalised patch under `availability` (compatibility). Response `data: <whole profile>`,
  `message: "Action completed"`.
* `PATCH /ambassadors/:id` is admin (any field, merged) or the ambassador themself (profile `userId` = caller). Owners may send
  only `bio` (text, max 2000), `languages[]`, `specialties[]` (max 30 items of up to 60 chars) and `isActive` (bool); any other
  key (verification, scores, `userId`, ...) is ignored, and a body with no editable field is 400. Others get 403.
  Response `data: <whole profile>`, `message: "Record updated"`.

### Experience bookings

* `POST /experience-bookings/:id/cancel` body `{reason?}` (traveller who owns it, or admin) -> `data: { "status": "cancelled",
  "cancelledAt": "<RFC3339>" }`, `message: "Booking updated"`. The reason (trimmed, max 500 chars) is stored in
  `cancellation_reason`, `cancelled_at` is set and the booked seats are returned to the slot (`booked_count`),
  all in one transaction. Cancelling an already cancelled booking is a no-op success (capacity is released once);
  `completed` / `refunded` bookings return 409 `invalid_state`. No refund is calculated or recorded. The response is
  not a booking object (the app handles that and returns `null`). `PATCH /:id/status` and `POST /:id/refund` still use
  the plain status transition (no capacity release, no reason).
* `GET /experience-bookings` -> `data: [booking]` (bare array, not `{bookings}`); `GET /experience-bookings/:id` -> booking:
  `{ _id, bookingRef, experienceId, slotId, travelerId, travelerName, travelerEmail, travelerPhone, guests, total (rupees),
  status, paymentStatus, createdAt,` plus display fields `title, experienceTitle` (experience title), `date` (slot date
  `YYYY-MM-DD`), `startTime` (`HH:mm`), `meetingPoint` (`experiences.content.meetingPoint|meeting_point|meetingLocation`, `""`
  when the experience has none: there is no dedicated column), `city`, `photo` (first of `content.image|photo|coverImage|
  heroImage|thumbnail|images|photos|gallery`, else `""`), `guestCount` (= `guests`) `}`. No host name.
* `GET /bookings` and `GET /bookings/:id` (property stays) carry the same extra keys: `title`/`experienceTitle` = property name,
  `date` = check-in (`YYYY-MM-DD`), `startTime` = `""` (stays have none), `meetingPoint` = property address, `city`, `photo`
  (property `image`, else first of `images`), `guestCount`. Existing keys (`checkIn`, `checkOut`, `totalPrice`, ...) are unchanged.
* `POST /experience-bookings` `{slotId, guests, emergencyContact?, specialRequests?}` -> 201 `data: { _id, bookingRef, experienceId, slotId, guests, total, status: "pending_payment" }`;
  prices are recomputed server side. `POST /experience-bookings/lock` requires a token in PostgreSQL mode and `slotId` (the app sends it with
  `skipAuth` and may omit `slotId`).

### Traveller safety (`documentpg`, domain `travellersafeties`, owner `userId`)

* `GET /traveller-safety` -> `data: { profiles: [...], total, page, limit }`; `POST` body verbatim -> 201 record
  (`emergencyContacts[{name,relationship,phone,email}], medicalConditions, bloodGroup, allergies, fitnessLevel`, plus `userId`);
  `PATCH /traveller-safety/:id` merges (owner). Multiple profiles per user are allowed; the app uses the first.

### Support tickets (`supportpg`, table `support_tickets`)

* `GET /support-tickets` -> `data: { tickets: [ticket], total, page, limit }` (own; filters `status, priority, category`).
* `POST /support-tickets` `{category, subject, message, priority? (low|medium|high|urgent, default medium), bookingId?, experienceId?,
  attachments?}` -> 201 `data: ticket`.
* ticket = `{ _id, userId, userName (the account email), userEmail, bookingId, experienceId, category, subject, priority,
  status (open|in_progress|waiting_user|resolved|closed), assignedTo, messages:[{senderId, senderName, senderRole, message,
  attachments, createdAt}], resolvedAt, resolvedBy, createdAt, updatedAt }`.

### Bulk bookings and custom experience requests (`documentpg`, owner `userId`)

* `GET /bulk-bookings` -> `data: { bookings: [...], total, page, limit }`; `POST /bulk-bookings` body verbatim
  (`bookingType, institutionName, coordinatorName, coordinatorPhone, coordinatorEmail, groupSize, ageGroup, budgetPerPerson?,
  destinationPreference, preferredDates, flexibleDates, experienceTypes[], specialRequirements, gstRequired, gstNumber`) -> 201 record.
  Optional `preferredDate` (`YYYY-MM-DD`) and `timingNote` (string <= 200, e.g. `"January, 2 days"`) are validated on create
  (400 `invalid_request` if malformed/too long; empty/null are dropped), trimmed, persisted in the document and returned in
  create responses, `GET` lists and `GET /:id`. The existing `preferredDates` field is kept as is. `PATCH` does not validate them.
* `GET /custom-experience-requests` -> `data: { requests: [...], total, page, limit }`; `POST` body verbatim
  (`title, category, preferredLocation, flexibleLocation, tripDuration, preferredDates, travellerCount, estimatedBudget,
  requirements[], description, contactPreferences[]`) -> 201 record. `preferredDate` / `timingNote` work exactly as for bulk bookings. Neither POST validates required fields in PostgreSQL mode.

---

## Mobile-client gaps found (not changed here)

1. ~~`host.ts getSlots` reads `data.slots`~~ Fixed server-side: `GET /experience-slots` returns `{slots, total}` and honours `month`.
2. `host.ts getMyHostProfile` expects `status`; the API sends `verificationStatus` (`pending_review` is not a value).
3. `PATCH /host-profiles/:id/safety-checklist` with `{key, isCompleted}` **replaces the whole checklist** with that object (server bug).
4. ~~Assignment actions do not change `status`; availability stored under `availability`~~ Fixed server-side (see the assignment table and availability above).
5. `bookings.ts getMyBookings` reads `data.bookings`; the API returns a bare array.
6. `createBooking` calls `/experience-bookings/lock` without a token; PostgreSQL mode requires one.
7. ~~`PATCH /ambassadors/:id` is admin-only~~ Fixed: owners can edit bio/languages/specialties/isActive. `GET /ambassadors` and `GET /ambassadors/:id` remain public.

## Configuration added

`UPLOAD_DIR` (default `./uploads`), `MAX_UPLOAD_BYTES` (default 10485760), `PUBLIC_BASE_URL` (optional, e.g. `https://api.viharam.in`).

## Verification

Static review only. The pure calculations and validators (`internal/modules/portalcalc`, incl. the assignment state machine, availability / profile patch validation, month bounds) and file-type sniffing
(`internal/platform/filetype`) have unit tests. SQL in the new handlers, route registration and the
upload handler tests need `go build/vet/test` with module downloads and a PostgreSQL database.
