// Package ambassadoractionspg implements the state-changing ambassador routes
// of the mobile app on top of the JSONB document store: the assignment
// accept / reject / proof / feedback actions, the availability update and the
// ambassador's own profile edit.
//
// Assignments (domain "ambassadorassignments") belong to an ambassador through
// owner_id or payload.ambassadorId; profiles (domain "ambassadorprofiles")
// through payload.userId or owner_id. Every handler reads the record with
// SELECT ... FOR UPDATE inside a transaction so concurrent actions cannot
// both pass the state check.
package ambassadoractionspg

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/modules/portalcalc"
	"github.com/viharam/go-api-backend/internal/platform/database"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

const (
	assignmentDomain = "ambassadorassignments"
	profileDomain    = "ambassadorprofiles"
)

// Handler serves the ambassador action routes.
type Handler struct {
	db *database.Postgres
	// adminUpdate performs the unrestricted profile merge for admins.
	adminUpdate gin.HandlerFunc
}

// NewHandler returns a Handler; adminUpdate is used by UpdateProfile when the
// caller is an admin.
func NewHandler(db *database.Postgres, adminUpdate gin.HandlerFunc) *Handler {
	return &Handler{db: db, adminUpdate: adminUpdate}
}

// record is a locked document_records row.
type record struct {
	id    string
	owner string
	doc   map[string]any
}

func isAdmin(claims *middleware.Claims) bool {
	return claims.Role == "Admin" || claims.Role == "SuperAdmin"
}

// begin starts a transaction and locks the record. When it returns false the
// error response has already been written.
func (h *Handler) begin(c *gin.Context, domain string) (pgx.Tx, *record, bool) {
	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not start transaction")
		return nil, nil, false
	}
	var owner *string
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT owner_id::text,payload FROM document_records WHERE id=$1::uuid AND domain=$2 FOR UPDATE`, c.Param("id"), domain).Scan(&owner, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(ctx)
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return nil, nil, false
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return nil, nil, false
	}
	rec := &record{id: c.Param("id"), doc: map[string]any{}}
	if owner != nil {
		rec.owner = *owner
	}
	if json.Unmarshal(raw, &rec.doc) != nil || rec.doc == nil {
		rec.doc = map[string]any{}
	}
	return tx, rec, true
}

// commit stores the record's document and responds with it, in the same shape
// as the generic document handlers (_id, createdAt, updatedAt added).
func (h *Handler) commit(c *gin.Context, tx pgx.Tx, rec *record, domain, message string) {
	ctx := c.Request.Context()
	raw, _ := json.Marshal(rec.doc)
	var created, updated string
	err := tx.QueryRow(ctx, `UPDATE document_records SET payload=$1,updated_at=now() WHERE id=$2::uuid AND domain=$3 RETURNING created_at::text,updated_at::text`, raw, rec.id, domain).Scan(&created, &updated)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update record")
		return
	}
	out := map[string]any{}
	for k, v := range rec.doc {
		out[k] = v
	}
	out["_id"], out["createdAt"], out["updatedAt"] = rec.id, created, updated
	_ = httpx.OK(c, out, message)
}

func rollback(ctx context.Context, tx pgx.Tx) { _ = tx.Rollback(ctx) }

func (r *record) assignedTo(userID string) bool {
	return userID != "" && (r.owner == userID || portalcalc.Text(r.doc["ambassadorId"]) == userID)
}

func (r *record) profileOf(userID string) bool {
	return userID != "" && (r.owner == userID || portalcalc.Text(r.doc["userId"]) == userID)
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// Accept handles POST /ambassador-assignments/:id/accept (assigned ambassador only).
func (h *Handler) Accept(c *gin.Context) { h.act(c, "accept") }

// Reject handles POST /ambassador-assignments/:id/reject.
func (h *Handler) Reject(c *gin.Context) { h.act(c, "reject") }

// Proof handles POST /ambassador-assignments/:id/proof.
func (h *Handler) Proof(c *gin.Context) { h.act(c, "proof") }

// Feedback handles POST /ambassador-assignments/:id/feedback, which is the
// completion report: it completes the assignment.
func (h *Handler) Feedback(c *gin.Context) { h.act(c, "feedback") }

func (h *Handler) act(c *gin.Context, action string) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	tx, rec, ok := h.begin(c, assignmentDomain)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	defer rollback(ctx, tx)

	// Accepting is the assigned ambassador's own decision; the other actions
	// may also be performed by an admin on their behalf.
	if !rec.assignedTo(claims.UserID) && (action == "accept" || !isAdmin(claims)) {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil || body == nil {
		body = map[string]any{}
	}
	current := portalcalc.Text(rec.doc["status"])
	next, allowed := portalcalc.AssignmentNextStatus(action, current)
	if !allowed {
		_ = httpx.Fail(c, 409, "invalid_state", "This action is not allowed while the assignment is "+statusLabel(current))
		return
	}

	stamp := now()
	switch action {
	case "accept":
		rec.doc["accept"] = body
		rec.doc["acceptedAt"] = stamp
	case "reject":
		rec.doc["reject"] = body
		rec.doc["rejectedAt"] = stamp
	case "proof":
		entries := portalcalc.ProofEntries(body, stamp)
		if len(entries) == 0 {
			_ = httpx.Fail(c, 400, "invalid_request", "url is required")
			return
		}
		rec.doc["proofs"] = portalcalc.AppendProofs(rec.doc, entries)
		rec.doc["proof"] = entries[len(entries)-1]
	case "feedback":
		rec.doc["feedback"] = body
		if fb := portalcalc.GuestFeedback(body, stamp); fb != nil {
			rec.doc["guestFeedback"] = fb
		}
		rec.doc["completedAt"] = stamp
	}
	rec.doc["status"] = next
	rec.doc["updatedAt"] = stamp
	h.commit(c, tx, rec, assignmentDomain, "Action completed")
}

func statusLabel(s string) string {
	if s == "" {
		return portalcalc.StatusPendingAcceptance
	}
	return s
}

// Availability handles PATCH /ambassadors/:id/availability for the ambassador
// themself or an admin. The validated fields are stored at the top level of
// the profile (where the app reads them) and under "availability".
func (h *Handler) Availability(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	tx, rec, ok := h.begin(c, profileDomain)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	defer rollback(ctx, tx)
	if !isAdmin(claims) && !rec.profileOf(claims.UserID) {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil || body == nil {
		body = map[string]any{}
	}
	patch, msg := portalcalc.AvailabilityPatch(body)
	if msg != "" {
		_ = httpx.Fail(c, 400, "invalid_request", msg)
		return
	}
	for k, v := range patch {
		rec.doc[k] = v
	}
	rec.doc["availability"] = patch
	rec.doc["updatedAt"] = now()
	h.commit(c, tx, rec, profileDomain, "Action completed")
}

// UpdateProfile handles PATCH /ambassadors/:id. Admins keep the unrestricted
// merge; an ambassador may edit only their own bio, languages, specialties
// and isActive flag. Verification, score and ownership fields are ignored.
func (h *Handler) UpdateProfile(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	if isAdmin(claims) {
		h.adminUpdate(c)
		return
	}
	tx, rec, ok := h.begin(c, profileDomain)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	defer rollback(ctx, tx)
	if !rec.profileOf(claims.UserID) {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil || body == nil {
		body = map[string]any{}
	}
	patch, msg := portalcalc.OwnerProfilePatch(body)
	if msg != "" {
		_ = httpx.Fail(c, 400, "invalid_request", msg)
		return
	}
	for k, v := range patch {
		rec.doc[k] = v
	}
	rec.doc["updatedAt"] = now()
	h.commit(c, tx, rec, profileDomain, "Record updated")
}
