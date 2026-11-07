package documentpg

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/modules/portalcalc"
	"github.com/viharam/go-api-backend/internal/platform/database"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

type Handler struct {
	db         *database.Postgres
	domain     string
	listKey    string
	adminOnly  bool
	ownerField string
}

func New(db *database.Postgres, domain, listKey, ownerField string, adminOnly bool) *Handler {
	return &Handler{db: db, domain: domain, listKey: listKey, ownerField: ownerField, adminOnly: adminOnly}
}

func (h *Handler) List(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if h.domain == "hostprofiles" && claims != nil && !isAdmin(claims) && claims.Role != "Host" {
		_ = httpx.Fail(c, 403, "forbidden", "Host access required")
		return
	}
	if h.domain == "ambassadorassignments" && claims != nil && !isAdmin(claims) && claims.Role != "Ambassador" {
		_ = httpx.Fail(c, 403, "forbidden", "Ambassador access required")
		return
	}
	if h.adminOnly && !isAdmin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	if claims == nil && h.ownerField != "" {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}

	where := []string{"domain=$1"}
	args := []any{h.domain}
	if h.domain == "experiencereviews" && !isAdmin(claims) {
		where = append(where, "payload->>'status'='approved'")
	}
	if h.domain == "reviews" && h.ownerField == "" {
		where = append(where, "COALESCE((payload->>'isPublished')::boolean,true)=true")
	}
	if h.ownerField != "" && !isAdmin(claims) {
		condition := "owner_id=$2::uuid"
		if h.domain == "disputes" {
			condition = "(owner_id=$2::uuid OR payload->>'againstId'=$2)"
		}
		where = append(where, condition)
		args = append(args, claims.UserID)
	}
	for _, key := range []string{"status", "category", "city", "state", "experienceId", "userId", "creatorType"} {
		if value := strings.TrimSpace(c.Query(key)); value != "" {
			where = append(where, fmt.Sprintf("payload->>'%s'=$%d", key, len(args)+1))
			args = append(args, value)
		}
	}
	page, limit := pageLimit(c)
	args = append(args, limit, (page-1)*limit)
	query := "SELECT id::text,owner_id::text,payload,created_at::text,updated_at::text FROM document_records WHERE " + strings.Join(where, " AND ") + fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := h.db.Pool.Query(c, query, args...)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch records")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		item, scanErr := scan(rows)
		if scanErr != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode records")
			return
		}
		items = append(items, item)
	}
	countArgs := args[:len(args)-2]
	var total int
	if err := h.db.Pool.QueryRow(c, "SELECT count(*) FROM document_records WHERE "+strings.Join(where, " AND "), countArgs...).Scan(&total); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to count records")
		return
	}
	_ = httpx.OK(c, gin.H{h.listKey: items, "total": total, "page": page, "limit": limit}, "")
}

func (h *Handler) Create(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	if h.adminOnly && !isAdmin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	if h.domain == "hostprofiles" && !isAdmin(claims) && claims.Role != "Host" {
		_ = httpx.Fail(c, 403, "forbidden", "Host access required")
		return
	}
	if h.domain == "ambassadorassignments" && !isAdmin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	if h.domain == "experiencereviews" && claims.Role != "Traveller" && !isAdmin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Traveller access required")
		return
	}
	if h.domain == "disputes" && claims.Role != "Traveller" && claims.Role != "Host" && !isAdmin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Only travellers or hosts can raise disputes")
		return
	}
	var payload map[string]any
	if err := c.ShouldBindJSON(&payload); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if message := validateCreate(h.domain, payload); message != "" {
		_ = httpx.Fail(c, 400, "invalid_request", message)
		return
	}
	if h.ownerField != "" && !isAdmin(claims) {
		payload[h.ownerField] = claims.UserID
	}
	if h.domain == "itineraries" || h.domain == "creatorprofiles" || h.domain == "travellersafeties" {
		payload[h.ownerField] = claims.UserID
	}
	raw, _ := json.Marshal(payload)
	var id, created, updated string
	owner := nullableOwner(payload, claims, h.ownerField)
	if h.domain == "hostprofiles" && !isAdmin(claims) {
		var existing int
		if err := h.db.Pool.QueryRow(c, "SELECT count(*) FROM document_records WHERE domain=$1 AND owner_id=$2::uuid", h.domain, owner).Scan(&existing); err == nil && existing > 0 {
			_ = httpx.Fail(c, 409, "conflict", "A host profile already exists for this user")
			return
		}
	}
	err := h.db.Pool.QueryRow(c, `INSERT INTO document_records(domain,owner_id,payload) VALUES($1,$2::uuid,$3) RETURNING id::text,created_at::text,updated_at::text`, h.domain, owner, raw).Scan(&id, &created, &updated)
	if err != nil {
		slog.Error("document create failed", "domain", h.domain, "owner", owner, "error", err)
		_ = httpx.Fail(c, 500, "database_error", "Failed to create record")
		return
	}
	result := enrich(payload, id, created, updated)
	_ = httpx.Created(c, result, "Record created")
}

func (h *Handler) Get(c *gin.Context) {
	item, claims, err := h.find(c, c.Param("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	if h.domain == "experiencereviews" && !isAdmin(claims) && stringValue(item["status"]) != "approved" {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	if h.domain == "reviews" && h.ownerField == "" && item["isPublished"] == false {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	if !h.canAccess(claims, item) {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	_ = httpx.OK(c, item, "")
}

func (h *Handler) Update(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	if h.adminOnly && !isAdmin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	current, owner, err := h.findRaw(c, c.Param("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	if !h.canAccess(claims, enrich(current, c.Param("id"), "", "")) || (h.ownerField != "" && !isAdmin(claims) && owner != claims.UserID) {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var patch map[string]any
	if err := c.ShouldBindJSON(&patch); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	delete(patch, "_id")
	delete(patch, "createdAt")
	delete(patch, "updatedAt")
	for key, value := range patch {
		current[key] = value
	}
	raw, _ := json.Marshal(current)
	var created, updated string
	err = h.db.Pool.QueryRow(c, "UPDATE document_records SET payload=$1,updated_at=now() WHERE id=$2::uuid AND domain=$3 RETURNING created_at::text,updated_at::text", raw, c.Param("id"), h.domain).Scan(&created, &updated)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update record")
		return
	}
	_ = httpx.OK(c, enrich(current, c.Param("id"), created, updated), "Record updated")
}

func (h *Handler) Delete(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	if h.adminOnly && !isAdmin(claims) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	_, owner, err := h.findRaw(c, c.Param("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	if h.adminOnly && !isAdmin(claims) || h.ownerField != "" && !isAdmin(claims) && owner != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	result, err := h.db.Pool.Exec(c, "DELETE FROM document_records WHERE id=$1::uuid AND domain=$2", c.Param("id"), h.domain)
	if err != nil || result.RowsAffected() == 0 {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	_ = httpx.OK(c, nil, "Record deleted")
}

func (h *Handler) Action(c *gin.Context)                     { h.action(c, c.Param("action")) }
func (h *Handler) ActionNamed(c *gin.Context, action string) { h.action(c, action) }
func (h *Handler) action(c *gin.Context, action string) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	item, owner, err := h.findRaw(c, c.Param("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	if !isAdmin(claims) && h.ownerField != "" && owner != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil {
		body = map[string]any{}
	}
	item[action] = body
	item["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	raw, _ := json.Marshal(item)
	if _, err := h.db.Pool.Exec(c, "UPDATE document_records SET payload=$1,updated_at=now() WHERE id=$2::uuid AND domain=$3", raw, c.Param("id"), h.domain); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update record")
		return
	}
	_ = httpx.OK(c, enrich(item, c.Param("id"), "", ""), "Action completed")
}

func (h *Handler) GetByToken(c *gin.Context) {
	var id, owner string
	var payload []byte
	err := h.db.Pool.QueryRow(c, "SELECT id::text,owner_id::text,payload FROM document_records WHERE domain=$1 AND payload->>'token'=$2", h.domain, c.Param("token")).Scan(&id, &owner, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch record")
		return
	}
	var value map[string]any
	_ = json.Unmarshal(payload, &value)
	_ = owner
	_ = httpx.OK(c, enrich(value, id, "", ""), "")
}

func (h *Handler) Availability(c *gin.Context) { h.Action(c) }
func (h *Handler) Waiver(c *gin.Context)       { h.Action(c) }

func (h *Handler) RootAction(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil {
		body = map[string]any{}
	}
	if body == nil {
		body = map[string]any{}
	}
	body["userId"] = claims.UserID
	raw, _ := json.Marshal(body)
	var id string
	if err := h.db.Pool.QueryRow(c, "INSERT INTO document_records(domain,owner_id,payload) VALUES($1,$2::uuid,$3) RETURNING id::text", h.domain, claims.UserID, raw).Scan(&id); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to save record")
		return
	}
	_ = httpx.Created(c, enrich(body, id, "", ""), "Record saved")
}

func (h *Handler) Validate(c *gin.Context) {
	if middleware.ClaimsFrom(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var body map[string]any
	_ = c.ShouldBindJSON(&body)
	if body == nil {
		body = map[string]any{}
	}
	_ = httpx.OK(c, gin.H{"valid": false, "code": body["code"]}, "Coupon validation requires a configured promotion rule")
}

func (h *Handler) find(c *gin.Context, id string) (map[string]any, *middleware.Claims, error) {
	value, _, err := h.findRaw(c, id)
	if err != nil {
		return nil, middleware.ClaimsFrom(c), err
	}
	return enrich(value, id, "", ""), middleware.ClaimsFrom(c), nil
}
func (h *Handler) findRaw(c *gin.Context, id string) (map[string]any, string, error) {
	var owner string
	var raw []byte
	err := h.db.Pool.QueryRow(c, "SELECT owner_id::text,payload FROM document_records WHERE id=$1::uuid AND domain=$2", id, h.domain).Scan(&owner, &raw)
	if err != nil {
		return nil, owner, err
	}
	value := map[string]any{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, owner, err
	}
	return value, owner, nil
}
func (h *Handler) canAccess(claims *middleware.Claims, item map[string]any) bool {
	if claims == nil {
		return h.ownerField == ""
	}
	if isAdmin(claims) || h.ownerField == "" {
		return true
	}
	if h.domain == "disputes" {
		return stringValue(item["raisedBy"]) == claims.UserID || stringValue(item["againstId"]) == claims.UserID
	}
	return stringValue(item[h.ownerField]) == claims.UserID
}
func isAdmin(c *middleware.Claims) bool {
	return c != nil && (c.Role == "Admin" || c.Role == "SuperAdmin")
}
func nullableOwner(payload map[string]any, c *middleware.Claims, field string) any {
	if field == "" || c == nil {
		return nil
	}
	if value := stringValue(payload[field]); value != "" {
		return value
	}
	return c.UserID
}
func stringValue(v any) string { s, _ := v.(string); return s }
func validateCreate(domain string, payload map[string]any) string {
	missing := func(fields ...string) string {
		for _, field := range fields {
			if value, ok := payload[field]; !ok || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
				return field
			}
		}
		return ""
	}
	switch domain {
	case "bulkbookings", "customexperiencerequests":
		// preferredDate (YYYY-MM-DD) and timingNote (<=200) are optional.
		return portalcalc.NormalizeTiming(payload)
	case "hostprofiles":
		if field := missing("displayName", "hostType", "phone"); field != "" {
			return field + " is required"
		}
	case "supporttickets":
		if field := missing("category", "subject", "message"); field != "" {
			return field + " is required"
		}
	case "incidents":
		if field := missing("reporterName", "type", "severity", "description", "occurredAt"); field != "" {
			return field + " is required"
		}
	case "experiencereviews":
		if field := missing("bookingId", "subjectType", "subjectId", "ratings", "title", "comment"); field != "" {
			return field + " is required"
		}
	case "reviews":
		if field := missing("bookingId", "rating", "comment"); field != "" {
			return field + " is required"
		}
	case "coupons":
		if field := missing("code", "description", "type", "value", "validFrom", "validTo"); field != "" {
			return field + " is required"
		}
	case "refundrequests":
		if field := missing("bookingId", "bookingType", "reason", "requestedAmount"); field != "" {
			return field + " is required"
		}
	case "disputes":
		if field := missing("bookingId", "againstId", "type", "description", "requestedResolution"); field != "" {
			return field + " is required"
		}
	case "cancellations":
		if field := missing("bookingId", "bookingType", "reason"); field != "" {
			return field + " is required"
		}
	}
	return ""
}
func enrich(payload map[string]any, id, created, updated string) map[string]any {
	result := map[string]any{}
	for key, value := range payload {
		result[key] = value
	}
	result["_id"] = id
	if created != "" {
		result["createdAt"] = created
	}
	if updated != "" {
		result["updatedAt"] = updated
	}
	return result
}
func scan(row pgx.Row) (map[string]any, error) {
	var id, owner, created, updated string
	var raw []byte
	if err := row.Scan(&id, &owner, &raw, &created, &updated); err != nil {
		return nil, err
	}
	value := map[string]any{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	_ = owner
	return enrich(value, id, created, updated), nil
}
func pageLimit(c *gin.Context) (int, int) {
	page, limit := 1, 20
	if _, err := fmt.Sscan(c.Query("page"), &page); err != nil || page < 1 {
		page = 1
	}
	if _, err := fmt.Sscan(c.Query("limit"), &limit); err != nil || limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return page, limit
}
