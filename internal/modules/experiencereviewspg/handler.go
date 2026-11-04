package experiencereviewspg

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/platform/database"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

type Handler struct{ db *database.Postgres }

func NewHandler(db *database.Postgres) *Handler { return &Handler{db: db} }

const selectReview = `SELECT id::text,traveler_id::text,traveler_name,booking_id,subject_type,subject_id,ratings,title,comment,photos,status,COALESCE(moderation_note,''),COALESCE(moderated_by,''),moderated_at::text,reports,helpful_votes,created_at::text,updated_at::text FROM experience_reviews`

func (h *Handler) List(c *gin.Context) {
	where := []string{"status='approved'"}
	args := []any{}
	for _, key := range []string{"subject_type", "subject_id", "traveler_id"} {
		if v := strings.TrimSpace(c.Query(map[string]string{"subject_type": "subjectType", "subject_id": "subjectId", "traveler_id": "travelerId"}[key])); v != "" {
			where = append(where, fmt.Sprintf("%s=$%d", key, len(args)+1))
			args = append(args, v)
		}
	}
	rows, err := h.db.Pool.Query(c, selectReview+" WHERE "+strings.Join(where, " AND ")+" ORDER BY created_at DESC", args...)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch reviews")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		item, e := scan(rows)
		if e != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode reviews")
			return
		}
		items = append(items, item)
	}
	_ = httpx.OK(c, items, "")
}

func (h *Handler) Create(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	if claims.Role != "Traveller" && claims.Role != "Admin" && claims.Role != "SuperAdmin" {
		_ = httpx.Fail(c, 403, "forbidden", "Traveller access required")
		return
	}
	var b map[string]any
	if c.ShouldBindJSON(&b) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	for _, key := range []string{"bookingId", "subjectType", "subjectId", "ratings", "title", "comment"} {
		if text(b[key]) == "" {
			_ = httpx.Fail(c, 400, "invalid_request", key+" is required")
			return
		}
	}
	subject := text(b["subjectType"])
	if !allowed(subject, "experience", "host", "ambassador") {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid subjectType")
		return
	}
	ratings := b["ratings"].(map[string]any)
	for _, key := range []string{"overall", "guide", "communication", "value", "safety"} {
		n, ok := ratings[key].(float64)
		if !ok || n < 1 || n > 5 {
			_ = httpx.Fail(c, 400, "invalid_request", "Ratings must be between 1 and 5")
			return
		}
	}
	var bookingTraveler, bookingName, status string
	err := h.db.Pool.QueryRow(c, "SELECT traveler_id::text,traveler_name,status FROM experience_bookings WHERE id=$1::uuid", text(b["bookingId"])).Scan(&bookingTraveler, &bookingName, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	if bookingTraveler != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "You can only review your own bookings")
		return
	}
	if status != "completed" {
		_ = httpx.Fail(c, 400, "invalid_request", "You can only review completed bookings")
		return
	}
	var exists bool
	if err := h.db.Pool.QueryRow(c, "SELECT EXISTS(SELECT 1 FROM experience_reviews WHERE booking_id=$1)", text(b["bookingId"])).Scan(&exists); err == nil && exists {
		_ = httpx.Fail(c, 409, "conflict", "You have already submitted a review for this booking and subject")
		return
	}
	ratings, ok := b["ratings"].(map[string]any)
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_request", "ratings must be an object")
		return
	}
	ratingsRaw, _ := json.Marshal(ratings)
	photosRaw, _ := json.Marshal(orArray(b["photos"]))
	var id string
	err = h.db.Pool.QueryRow(c, `INSERT INTO experience_reviews(traveler_id,traveler_name,booking_id,subject_type,subject_id,ratings,title,comment,photos) VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id::text`, claims.UserID, bookingName, text(b["bookingId"]), subject, text(b["subjectId"]), ratingsRaw, text(b["title"]), text(b["comment"]), photosRaw).Scan(&id)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to submit review")
		return
	}
	item, _ := h.get(c, id)
	_ = httpx.Created(c, item, "Review submitted")
}

func (h *Handler) Get(c *gin.Context) {
	item, err := h.get(c, c.Param("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Review not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid review ID")
		return
	}
	_ = httpx.OK(c, item, "")
}
func (h *Handler) Moderate(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || !admin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	var b map[string]any
	if c.ShouldBindJSON(&b) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	sets := []string{}
	args := []any{}
	if v, ok := b["status"]; ok {
		status := text(v)
		if !allowed(status, "pending", "approved", "rejected", "flagged") {
			_ = httpx.Fail(c, 400, "invalid_request", "Invalid review status")
			return
		}
		sets = append(sets, "status=$"+itoa(len(args)+1))
		args = append(args, status)
	}
	if v, ok := b["moderationNote"]; ok {
		sets = append(sets, "moderation_note=$"+itoa(len(args)+1))
		args = append(args, text(v))
	}
	sets = append(sets, "moderated_by=$"+itoa(len(args)+1), "moderated_at=now()")
	args = append(args, claims.Email)
	args = append(args, c.Param("id"))
	var id string
	if err := h.db.Pool.QueryRow(c, "UPDATE experience_reviews SET "+strings.Join(sets, ",")+",updated_at=now() WHERE id=$"+itoa(len(args))+"::uuid RETURNING id::text", args...).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Review not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update review")
		return
	}
	item, _ := h.get(c, id)
	_ = httpx.OK(c, item, "Review updated")
}
func (h *Handler) Delete(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || !admin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	result, err := h.db.Pool.Exec(c, "DELETE FROM experience_reviews WHERE id=$1::uuid", c.Param("id"))
	if err != nil || result.RowsAffected() == 0 {
		_ = httpx.Fail(c, 404, "not_found", "Review not found")
		return
	}
	_ = httpx.OK(c, nil, "Review deleted")
}
func (h *Handler) Helpful(c *gin.Context) {
	if middleware.ClaimsFrom(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var id string
	if err := h.db.Pool.QueryRow(c, "UPDATE experience_reviews SET helpful_votes=helpful_votes+1,updated_at=now() WHERE id=$1::uuid RETURNING id::text", c.Param("id")).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Review not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to mark review as helpful")
		return
	}
	item, _ := h.get(c, id)
	_ = httpx.OK(c, item, "Marked as helpful")
}
func (h *Handler) Report(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var b map[string]any
	if c.ShouldBindJSON(&b) != nil || text(b["reason"]) == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "reason is required")
		return
	}
	var traveler string
	var reports []byte
	if err := h.db.Pool.QueryRow(c, "SELECT traveler_id::text,reports FROM experience_reviews WHERE id=$1::uuid", c.Param("id")).Scan(&traveler, &reports); errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Review not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid review ID")
		return
	}
	if traveler == claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "You cannot report your own review")
		return
	}
	var list []any
	_ = json.Unmarshal(reports, &list)
	entry := map[string]any{"reportedBy": claims.UserID, "reason": strings.TrimSpace(text(b["reason"])), "reportedAt": time.Now().UTC()}
	list = append(list, entry)
	raw, _ := json.Marshal(list)
	status := "approved"
	if len(list) >= 3 {
		status = "flagged"
	}
	var id string
	if err := h.db.Pool.QueryRow(c, "UPDATE experience_reviews SET reports=$1::jsonb,status=$2,updated_at=now() WHERE id=$3::uuid RETURNING id::text", raw, status, c.Param("id")).Scan(&id); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to report review")
		return
	}
	item, _ := h.get(c, id)
	_ = httpx.Created(c, item, "Review reported")
}
func (h *Handler) get(c *gin.Context, id string) (map[string]any, error) {
	return scan(h.db.Pool.QueryRow(c, selectReview+" WHERE id=$1::uuid", id))
}
func scan(row pgx.Row) (map[string]any, error) {
	var id, traveler, name, booking, subjectType, subjectID, title, comment, status, note, moderatedBy, created, updated string
	var moderatedAt *string
	var ratings, photos, reports []byte
	var helpful int
	if err := row.Scan(&id, &traveler, &name, &booking, &subjectType, &subjectID, &ratings, &title, &comment, &photos, &status, &note, &moderatedBy, &moderatedAt, &reports, &helpful, &created, &updated); err != nil {
		return nil, err
	}
	return map[string]any{"_id": id, "travelerId": traveler, "travelerName": name, "bookingId": booking, "subjectType": subjectType, "subjectId": subjectID, "ratings": decode(ratings), "title": title, "comment": comment, "photos": decode(photos), "status": status, "moderationNote": note, "moderatedBy": moderatedBy, "moderatedAt": moderatedAt, "reports": decode(reports), "helpfulVotes": helpful, "createdAt": created, "updatedAt": updated}, nil
}
func admin(c *middleware.Claims) bool {
	return c != nil && (c.Role == "Admin" || c.Role == "SuperAdmin")
}
func text(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }
func allowed(v string, values ...string) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}
func orArray(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}
func decode(b []byte) any {
	var v any
	if len(b) > 0 && json.Unmarshal(b, &v) == nil {
		return v
	}
	return []any{}
}
func itoa(v int) string { return fmt.Sprint(v) }
