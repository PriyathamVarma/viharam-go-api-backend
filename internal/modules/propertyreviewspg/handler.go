package propertyreviewspg

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/platform/database"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

type Handler struct{ db *database.Postgres }

func NewHandler(db *database.Postgres) *Handler { return &Handler{db: db} }

const selectReview = `SELECT id::text,traveler_id::text,property_id::text,booking_id::text,rating,comment,is_published,created_at::text,updated_at::text FROM property_reviews`

func (h *Handler) List(c *gin.Context) {
	where := []string{"is_published=true"}
	args := []any{}
	if property := strings.TrimSpace(c.Query("propertyId")); property != "" {
		where = append(where, fmt.Sprintf("property_id=$%d::uuid", len(args)+1))
		args = append(args, property)
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
	var b map[string]any
	if c.ShouldBindJSON(&b) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	booking := text(b["bookingId"])
	comment := text(b["comment"])
	rating := number(b["rating"])
	if booking == "" || comment == "" || rating < 1 {
		_ = httpx.Fail(c, 400, "invalid_request", "bookingId, rating and comment are required")
		return
	}
	if rating > 5 {
		_ = httpx.Fail(c, 400, "invalid_request", "Rating must be between 1 and 5")
		return
	}
	var traveler, property, status string
	if err := h.db.Pool.QueryRow(c, "SELECT traveler_id::text,property_id::text,status FROM property_bookings WHERE id=$1::uuid", booking).Scan(&traveler, &property, &status); errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Booking not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	if traveler != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "You can only review your own bookings")
		return
	}
	if status != "completed" {
		_ = httpx.Fail(c, 400, "invalid_request", "You can only review completed bookings")
		return
	}
	tx, err := h.db.Pool.Begin(c)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not start review transaction")
		return
	}
	defer tx.Rollback(c)
	var id string
	if err := tx.QueryRow(c, "INSERT INTO property_reviews(traveler_id,property_id,booking_id,rating,comment) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5) RETURNING id::text", claims.UserID, property, booking, rating, comment).Scan(&id); err != nil {
		_ = httpx.Fail(c, 409, "conflict", "A review already exists for this booking")
		return
	}
	if _, err := tx.Exec(c, "UPDATE properties SET rating=COALESCE((SELECT round(avg(rating)::numeric,1) FROM property_reviews WHERE property_id=$1::uuid AND is_published=true),0),review_count=(SELECT count(*) FROM property_reviews WHERE property_id=$1::uuid AND is_published=true),updated_at=now() WHERE id=$1::uuid", property); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update property rating")
		return
	}
	if err := tx.Commit(c); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not commit review")
		return
	}
	item, _ := h.get(c, id)
	_ = httpx.Created(c, item, "Review submitted")
}

func (h *Handler) Delete(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var traveler, property string
	if err := h.db.Pool.QueryRow(c, "SELECT traveler_id::text,property_id::text FROM property_reviews WHERE id=$1::uuid", c.Param("id")).Scan(&traveler, &property); errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Review not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	if traveler != claims.UserID && !admin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	tx, err := h.db.Pool.Begin(c)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not start delete transaction")
		return
	}
	defer tx.Rollback(c)
	if _, err := tx.Exec(c, "DELETE FROM property_reviews WHERE id=$1::uuid", c.Param("id")); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to delete review")
		return
	}
	if _, err := tx.Exec(c, "UPDATE properties SET rating=COALESCE((SELECT round(avg(rating)::numeric,1) FROM property_reviews WHERE property_id=$1::uuid AND is_published=true),0),review_count=(SELECT count(*) FROM property_reviews WHERE property_id=$1::uuid AND is_published=true),updated_at=now() WHERE id=$1::uuid", property); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update property rating")
		return
	}
	if err := tx.Commit(c); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not commit deletion")
		return
	}
	_ = httpx.OK(c, nil, "Review deleted")
}

func (h *Handler) get(c *gin.Context, id string) (map[string]any, error) {
	return scan(h.db.Pool.QueryRow(c, selectReview+" WHERE id=$1::uuid", id))
}
func scan(row pgx.Row) (map[string]any, error) {
	var id, traveler, property, booking, comment, created, updated string
	var rating float64
	var published bool
	if err := row.Scan(&id, &traveler, &property, &booking, &rating, &comment, &published, &created, &updated); err != nil {
		return nil, err
	}
	return map[string]any{"_id": id, "travelerId": traveler, "propertyId": property, "bookingId": booking, "rating": rating, "comment": comment, "isPublished": published, "createdAt": created, "updatedAt": updated}, nil
}
func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
func text(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }
func admin(c *middleware.Claims) bool {
	return c != nil && (c.Role == "Admin" || c.Role == "SuperAdmin")
}
