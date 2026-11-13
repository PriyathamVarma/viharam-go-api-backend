package supportpg

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

const selectTicket = `SELECT id::text,user_id::text,user_name,user_email,COALESCE(booking_id,''),COALESCE(experience_id,''),category,subject,priority,status,COALESCE(assigned_to,''),messages,admin_notes,resolved_at::text,COALESCE(resolved_by,''),created_at::text,updated_at::text FROM support_tickets`

func (h *Handler) List(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	where, args := []string{}, []any{}
	if !admin(claims) {
		where = append(where, "user_id=$1::uuid")
		args = append(args, claims.UserID)
	}
	for _, key := range []string{"status", "priority", "category"} {
		if value := strings.TrimSpace(c.Query(key)); value != "" {
			where = append(where, key+"=$"+itoa(len(args)+1))
			args = append(args, value)
		}
	}
	page, limit := pageLimit(c)
	query := selectTicket
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT $" + itoa(len(args)+1) + " OFFSET $" + itoa(len(args)+2)
	args = append(args, limit, (page-1)*limit)
	rows, err := h.db.Pool.Query(c, query, args...)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch support tickets")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		item, e := scan(rows, admin(claims))
		if e != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode support tickets")
			return
		}
		items = append(items, item)
	}
	countArgs := args[:len(args)-2]
	var total int
	countQuery := "SELECT count(*) FROM support_tickets"
	if len(where) > 0 {
		countQuery += " WHERE " + strings.Join(where, " AND ")
	}
	if err := h.db.Pool.QueryRow(c, countQuery, countArgs...).Scan(&total); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to count support tickets")
		return
	}
	_ = httpx.OK(c, gin.H{"tickets": items, "total": total, "page": page, "limit": limit}, "")
}

func (h *Handler) Create(c *gin.Context) {
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
	category, subject, message := text(body["category"]), text(body["subject"]), text(body["message"])
	if category == "" || subject == "" || message == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "category, subject and message are required")
		return
	}
	priority := text(body["priority"])
	if priority == "" {
		priority = "medium"
	}
	if !allowed(priority, "low", "medium", "high", "urgent") {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid priority")
		return
	}
	initial := map[string]any{"senderId": claims.UserID, "senderName": claims.Email, "senderRole": "user", "message": message, "attachments": array(body["attachments"]), "createdAt": time.Now().UTC()}
	messages, _ := json.Marshal([]any{initial})
	var id string
	// Pass JSONB as text. pgx can encode []byte as bytea, which PostgreSQL
	// rejects when the target column is jsonb.
	err := h.db.Pool.QueryRow(c, `INSERT INTO support_tickets(user_id,user_name,user_email,booking_id,experience_id,category,subject,priority,messages) VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9::jsonb) RETURNING id::text`, claims.UserID, claims.Email, claims.Email, text(body["bookingId"]), text(body["experienceId"]), category, subject, priority, string(messages)).Scan(&id)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to create support ticket")
		return
	}
	item, _ := h.get(c, id, true)
	_ = httpx.Created(c, item, "Support ticket created")
}

func (h *Handler) Get(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	item, err := h.get(c, c.Param("id"), admin(claims))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Ticket not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	if !admin(claims) && item["userId"] != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	_ = httpx.OK(c, item, "")
}

func (h *Handler) Update(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || !admin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	current, err := h.get(c, c.Param("id"), true)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Ticket not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	status := text(body["status"])
	if status == "" {
		status = text(current["status"])
	}
	priority := text(body["priority"])
	if priority == "" {
		priority = text(current["priority"])
	}
	if !allowed(status, "open", "in_progress", "waiting_user", "resolved", "closed") || !allowed(priority, "low", "medium", "high", "urgent") {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid status or priority")
		return
	}
	system := map[string]any{"senderId": "system", "senderName": "System", "senderRole": "system", "message": "Ticket status updated to " + status + " by admin.", "attachments": []any{}, "createdAt": time.Now().UTC()}
	raw, _ := json.Marshal([]any{system})
	var resolved any
	if status == "resolved" {
		resolved = time.Now().UTC()
	}
	var resolvedBy any
	if status == "resolved" {
		resolvedBy = claims.Email
	}
	if _, err := h.db.Pool.Exec(c, "UPDATE support_tickets SET status=$1,priority=$2,assigned_to=COALESCE($3,assigned_to),resolved_at=CASE WHEN $1='resolved' THEN COALESCE(resolved_at,now()) ELSE resolved_at END,resolved_by=CASE WHEN $1='resolved' THEN $4 ELSE resolved_by END,messages=messages||$5::jsonb,updated_at=now() WHERE id=$6::uuid", status, priority, nullable(body, "assignedTo"), resolvedBy, string(raw), c.Param("id")); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update ticket")
		return
	}
	updated, _ := h.get(c, c.Param("id"), true)
	_ = resolved
	_ = httpx.OK(c, updated, "Ticket updated")
}

func (h *Handler) Message(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	item, err := h.get(c, c.Param("id"), true)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Ticket not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	if !admin(claims) && item["userId"] != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil || text(body["message"]) == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "message is required")
		return
	}
	role := "user"
	status := text(item["status"])
	if admin(claims) {
		role = "admin"
		status = "waiting_user"
	} else if status == "waiting_user" {
		status = "in_progress"
	}
	msg := map[string]any{"senderId": claims.UserID, "senderName": claims.Email, "senderRole": role, "message": text(body["message"]), "attachments": array(body["attachments"]), "createdAt": time.Now().UTC()}
	raw, _ := json.Marshal([]any{msg})
	if _, err := h.db.Pool.Exec(c, "UPDATE support_tickets SET messages=messages||$1::jsonb,status=$2,updated_at=now() WHERE id=$3::uuid", string(raw), status, c.Param("id")); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to send message")
		return
	}
	updated, _ := h.get(c, c.Param("id"), admin(claims))
	_ = httpx.OK(c, updated, "Message sent")
}

func (h *Handler) AdminNote(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || !admin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil || text(body["note"]) == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "note is required")
		return
	}
	note := map[string]any{"adminEmail": claims.Email, "note": text(body["note"]), "createdAt": time.Now().UTC()}
	raw, _ := json.Marshal([]any{note})
	var exists bool
	if err := h.db.Pool.QueryRow(c, "SELECT EXISTS(SELECT 1 FROM support_tickets WHERE id=$1::uuid)", c.Param("id")).Scan(&exists); err != nil || !exists {
		_ = httpx.Fail(c, 404, "not_found", "Ticket not found")
		return
	}
	if _, err := h.db.Pool.Exec(c, "UPDATE support_tickets SET admin_notes=admin_notes||$1::jsonb,updated_at=now() WHERE id=$2::uuid", string(raw), c.Param("id")); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to add admin note")
		return
	}
	_ = httpx.Created(c, note, "Admin note added")
}

func (h *Handler) get(c *gin.Context, id string, includeNotes bool) (map[string]any, error) {
	return scan(h.db.Pool.QueryRow(c, selectTicket+" WHERE id=$1::uuid", id), includeNotes)
}
func scan(row pgx.Row, includeNotes bool) (map[string]any, error) {
	var id, userID, userName, userEmail, bookingID, experienceID, category, subject, priority, status, assigned string
	var messages, notes []byte
	var resolvedAt *string
	var resolvedBy, created, updated string
	if err := row.Scan(&id, &userID, &userName, &userEmail, &bookingID, &experienceID, &category, &subject, &priority, &status, &assigned, &messages, &notes, &resolvedAt, &resolvedBy, &created, &updated); err != nil {
		return nil, err
	}
	item := map[string]any{"_id": id, "userId": userID, "userName": userName, "userEmail": userEmail, "bookingId": bookingID, "experienceId": experienceID, "category": category, "subject": subject, "priority": priority, "status": status, "assignedTo": assigned, "messages": decode(messages), "resolvedAt": resolvedAt, "resolvedBy": resolvedBy, "createdAt": created, "updatedAt": updated}
	if includeNotes {
		item["adminNotes"] = decode(notes)
	}
	return item, nil
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
func nullable(b map[string]any, k string) any {
	if v, ok := b[k]; ok {
		return text(v)
	}
	return nil
}
func array(v any) any {
	if a, ok := v.([]any); ok {
		return a
	}
	return []any{}
}
func decode(raw []byte) any {
	var v any
	if len(raw) > 0 && json.Unmarshal(raw, &v) == nil {
		return v
	}
	return []any{}
}
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	out := ""
	for v > 0 {
		out = string(rune('0'+v%10)) + out
		v /= 10
	}
	return out
}
func pageLimit(c *gin.Context) (int, int) {
	page, limit := 1, 20
	_, _ = fmt.Sscan(c.Query("page"), &page)
	_, _ = fmt.Sscan(c.Query("limit"), &limit)
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return page, limit
}
