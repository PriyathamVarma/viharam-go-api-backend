package incidentspg

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

const selectIncident = `SELECT id::text,reporter_id::text,reporter_name,COALESCE(booking_id,''),COALESCE(experience_id,''),type,severity,description,location,occurred_at::text,photos,status,COALESCE(assigned_to,''),COALESCE(resolution,''),resolved_at::text,created_at::text,updated_at::text FROM incidents`

func (h *Handler) List(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || !admin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	where := []string{}
	args := []any{}
	for _, key := range []string{"status", "severity"} {
		if v := strings.TrimSpace(c.Query(key)); v != "" {
			where = append(where, fmt.Sprintf("%s=$%d", key, len(args)+1))
			args = append(args, v)
		}
	}
	if v := strings.TrimSpace(c.Query("reporterId")); v != "" {
		where = append(where, fmt.Sprintf("reporter_id=$%d::uuid", len(args)+1))
		args = append(args, v)
	}
	query := selectIncident
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC"
	rows, err := h.db.Pool.Query(c, query, args...)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch incidents")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		item, e := scan(rows)
		if e != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode incidents")
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
	for _, key := range []string{"reporterName", "type", "severity", "description", "occurredAt"} {
		if text(b[key]) == "" {
			_ = httpx.Fail(c, 400, "invalid_request", "reporterName, type, severity, description, and occurredAt are required")
			return
		}
	}
	occurred, err := time.Parse(time.RFC3339, text(b["occurredAt"]))
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "occurredAt must be a valid ISO timestamp")
		return
	}
	location, _ := json.Marshal(orObject(b["location"]))
	photos, _ := json.Marshal(orArray(b["photos"]))
	var id string
	err = h.db.Pool.QueryRow(c, `INSERT INTO incidents(reporter_id,reporter_name,booking_id,experience_id,type,severity,description,location,occurred_at,photos) VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text`, claims.UserID, text(b["reporterName"]), text(b["bookingId"]), text(b["experienceId"]), text(b["type"]), text(b["severity"]), text(b["description"]), location, occurred, photos).Scan(&id)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to report incident")
		return
	}
	item, _ := h.get(c, id)
	_ = httpx.Created(c, item, "Incident reported")
}

func (h *Handler) Get(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	item, err := h.get(c, c.Param("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Incident not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	if !admin(claims) && item["reporterId"] != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	_ = httpx.OK(c, item, "")
}

func (h *Handler) Update(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || !admin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var b map[string]any
	if c.ShouldBindJSON(&b) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	sets := []string{}
	args := []any{}
	for _, key := range []string{"status", "assignedTo", "resolution"} {
		if v, ok := b[key]; ok {
			col := map[string]string{"assignedTo": "assigned_to"}[key]
			if col == "" {
				col = key
			}
			sets = append(sets, fmt.Sprintf("%s=$%d", col, len(args)+1))
			args = append(args, text(v))
		}
	}
	if v, ok := b["resolvedAt"]; ok {
		parsed, err := time.Parse(time.RFC3339, text(v))
		if err != nil {
			_ = httpx.Fail(c, 400, "invalid_request", "resolvedAt must be a valid ISO timestamp")
			return
		}
		sets = append(sets, fmt.Sprintf("resolved_at=$%d", len(args)+1))
		args = append(args, parsed)
	}
	if len(sets) == 0 {
		_ = httpx.Fail(c, 400, "invalid_request", "No update fields supplied")
		return
	}
	args = append(args, c.Param("id"))
	var id string
	if err := h.db.Pool.QueryRow(c, "UPDATE incidents SET "+strings.Join(sets, ",")+",updated_at=now() WHERE id=$"+itoa(len(args))+"::uuid RETURNING id::text", args...).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Incident not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update incident")
		return
	}
	item, _ := h.get(c, id)
	_ = httpx.OK(c, item, "Incident updated")
}

func (h *Handler) get(c *gin.Context, id string) (map[string]any, error) {
	return scan(h.db.Pool.QueryRow(c, selectIncident+" WHERE id=$1::uuid", id))
}
func scan(row pgx.Row) (map[string]any, error) {
	var id, reporter, reporterName, booking, experience, typ, severity, description, occurred, status, assigned, resolution, created, updated string
	var location, photos []byte
	var resolved *string
	if err := row.Scan(&id, &reporter, &reporterName, &booking, &experience, &typ, &severity, &description, &location, &occurred, &photos, &status, &assigned, &resolution, &resolved, &created, &updated); err != nil {
		return nil, err
	}
	return map[string]any{"_id": id, "reporterId": reporter, "reporterName": reporterName, "bookingId": booking, "experienceId": experience, "type": typ, "severity": severity, "description": description, "location": decode(location), "occurredAt": occurred, "photos": decode(photos), "status": status, "assignedTo": assigned, "resolution": resolution, "resolvedAt": resolved, "createdAt": created, "updatedAt": updated}, nil
}
func admin(c *middleware.Claims) bool {
	return c != nil && (c.Role == "Admin" || c.Role == "SuperAdmin")
}
func text(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }
func orArray(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}
func orObject(v any) any {
	if v == nil {
		return map[string]any{}
	}
	return v
}
func decode(b []byte) any {
	var v any
	if len(b) > 0 && json.Unmarshal(b, &v) == nil {
		return v
	}
	return map[string]any{}
}
func itoa(v int) string { return fmt.Sprint(v) }
