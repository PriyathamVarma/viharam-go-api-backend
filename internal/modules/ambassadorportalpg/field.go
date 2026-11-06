package ambassadorportalpg

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/modules/portalcalc"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

// loadAssignment fetches an assignment document and checks that the caller is
// its ambassador or an admin. On failure it has already written the response.
func (h *Handler) loadAssignment(c *gin.Context, claims *middleware.Claims) (map[string]any, bool) {
	var owner *string
	var raw []byte
	err := h.db.Pool.QueryRow(c.Request.Context(), `SELECT owner_id::text,payload FROM document_records WHERE id=$1::uuid AND domain='ambassadorassignments'`, c.Param("id")).Scan(&owner, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Assignment not found")
		return nil, false
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return nil, false
	}
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		doc = map[string]any{}
	}
	admin := claims.Role == "Admin" || claims.Role == "SuperAdmin"
	assigned := (owner != nil && *owner == claims.UserID) || portalcalc.Text(doc["ambassadorId"]) == claims.UserID
	if !admin && !assigned {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return nil, false
	}
	return doc, true
}

// MessageGuests handles POST /api/v1/ambassador-assignments/:id/message-guests.
// The platform has no notifications table or push channel, so the message is
// stored in assignment_messages; "sent" is the number of travellers on the
// linked booking (0 when the booking cannot be found) who will see it.
func (h *Handler) MessageGuests(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	doc, ok := h.loadAssignment(c, claims)
	if !ok {
		return
	}
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	text, _ := body["message"].(string)
	message, msg := portalcalc.ValidateMessage(text)
	if msg != "" {
		_ = httpx.Fail(c, 400, "invalid_request", msg)
		return
	}
	switch portalcalc.Text(doc["status"]) {
	case "rejected", "cancelled":
		_ = httpx.Fail(c, 409, "conflict", "This assignment is no longer active")
		return
	}
	sent := 0
	if bookingID := portalcalc.Text(doc["bookingId"]); bookingID != "" {
		if err := h.db.Pool.QueryRow(c.Request.Context(), `SELECT count(DISTINCT traveler_id) FROM experience_bookings WHERE id::text=$1`, bookingID).Scan(&sent); err != nil {
			sent = 0
		}
	}
	var id string
	if err := h.db.Pool.QueryRow(c.Request.Context(), `INSERT INTO assignment_messages(assignment_id,sender_user_id,body) VALUES($1::uuid,$2::uuid,$3) RETURNING id::text`, c.Param("id"), claims.UserID, message).Scan(&id); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to send message")
		return
	}
	_ = httpx.OK(c, gin.H{"sent": sent, "messageId": id}, "Message sent")
}

// Messages handles GET /api/v1/ambassador-assignments/:id/messages, newest first.
func (h *Handler) Messages(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	if _, ok := h.loadAssignment(c, claims); !ok {
		return
	}
	rows, err := h.db.Pool.Query(c.Request.Context(), `SELECT id::text,body,created_at FROM assignment_messages WHERE assignment_id=$1::uuid ORDER BY created_at DESC LIMIT 200`, c.Param("id"))
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch messages")
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, body string
		var created time.Time
		if err := rows.Scan(&id, &body, &created); err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode messages")
			return
		}
		items = append(items, gin.H{"id": id, "body": body, "createdAt": created.UTC().Format(time.RFC3339)})
	}
	if rows.Err() != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch messages")
		return
	}
	_ = httpx.OK(c, gin.H{"messages": items}, "")
}

// CreateVisit handles POST /api/v1/ambassador/visits.
func (h *Handler) CreateVisit(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	str := func(k string) string { s, _ := body[k].(string); return s }
	hostID := portalcalc.Text(body["hostProfileId"])
	if !portalcalc.IsUUID(hostID) {
		_ = httpx.Fail(c, 400, "invalid_request", "hostProfileId must be a valid id")
		return
	}
	day, msg := portalcalc.ValidateVisitDate(str("scheduledFor"), time.Now())
	if msg == "" {
		var at, notes string
		if at, msg = portalcalc.ValidateVisitTime(str("time")); msg == "" {
			if notes, msg = portalcalc.ValidateVisitNotes(str("notes")); msg == "" {
				h.insertVisit(c, claims.UserID, hostID, day, at, notes)
				return
			}
		}
	}
	_ = httpx.Fail(c, 400, "invalid_request", msg)
}

func (h *Handler) insertVisit(c *gin.Context, userID, hostID, day, at, notes string) {
	var id string
	err := h.db.Pool.QueryRow(c.Request.Context(), `INSERT INTO ambassador_visits(ambassador_user_id,host_profile_id,scheduled_for,scheduled_time,notes)
SELECT $1::uuid,hp.id,$3::date,NULLIF($4,'')::time,$5 FROM host_profiles hp WHERE hp.id=$2::uuid RETURNING id::text`, userID, hostID, day, at, notes).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Host profile not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to schedule visit")
		return
	}
	_ = httpx.Created(c, gin.H{"id": id, "hostProfileId": hostID, "scheduledFor": day, "time": at, "notes": notes, "status": "scheduled"}, "Visit scheduled")
}

// Visits handles GET /api/v1/ambassador/visits, ascending by date.
func (h *Handler) Visits(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	rows, err := h.db.Pool.Query(c.Request.Context(), `SELECT v.id::text,v.host_profile_id::text,hp.display_name,COALESCE(hp.address->>'village',''),
v.scheduled_for::text,COALESCE(to_char(v.scheduled_time,'HH24:MI'),''),v.notes,v.status
FROM ambassador_visits v JOIN host_profiles hp ON hp.id=v.host_profile_id
WHERE v.ambassador_user_id=$1::uuid ORDER BY v.scheduled_for,v.scheduled_time NULLS LAST,v.created_at LIMIT 500`, claims.UserID)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch visits")
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, host, name, village, day, at, notes, status string
		if err := rows.Scan(&id, &host, &name, &village, &day, &at, &notes, &status); err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode visits")
			return
		}
		items = append(items, gin.H{"id": id, "hostProfileId": host, "hostName": name, "village": village, "scheduledFor": day, "time": at, "notes": notes, "status": status})
	}
	if rows.Err() != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch visits")
		return
	}
	_ = httpx.OK(c, gin.H{"visits": items}, "")
}

// UpdateVisit handles PATCH /api/v1/ambassador/visits/:id {status}. Only the
// owner may change a visit, and only a scheduled visit can move to
// completed/cancelled (repeating the current status is a no-op success).
func (h *Handler) UpdateVisit(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	id := c.Param("id")
	if !portalcalc.IsUUID(id) {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	text, _ := body["status"].(string)
	status, msg := portalcalc.ValidateVisitStatus(text)
	if msg != "" {
		_ = httpx.Fail(c, 400, "invalid_request", msg)
		return
	}
	ctx := c.Request.Context()
	var owner, current string
	err := h.db.Pool.QueryRow(ctx, `SELECT ambassador_user_id::text,status FROM ambassador_visits WHERE id=$1::uuid`, id).Scan(&owner, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Visit not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch visit")
		return
	}
	if owner != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	if current != status && current != "scheduled" {
		_ = httpx.Fail(c, 409, "invalid_state", "Only a scheduled visit can be changed")
		return
	}
	var host, day, at, notes string
	err = h.db.Pool.QueryRow(ctx, `UPDATE ambassador_visits SET status=$2 WHERE id=$1::uuid
RETURNING host_profile_id::text,scheduled_for::text,COALESCE(to_char(scheduled_time,'HH24:MI'),''),notes`, id, status).Scan(&host, &day, &at, &notes)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update visit")
		return
	}
	_ = httpx.OK(c, gin.H{"id": id, "hostProfileId": host, "scheduledFor": day, "time": at, "notes": notes, "status": status}, "Visit updated")
}
