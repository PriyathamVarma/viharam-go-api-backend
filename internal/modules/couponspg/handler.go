package couponspg

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

const selectCoupon = `SELECT id::text,code,description,type,value,min_booking_amount,COALESCE(max_discount_amount,0),max_uses,used_count,applicable_experience_ids,valid_from::text,valid_to::text,is_active,usages,COALESCE(created_by::text,''),created_at::text,updated_at::text FROM coupons`

func (h *Handler) List(c *gin.Context) {
	if !h.admin(c) {
		return
	}
	where := []string{}
	args := []any{}
	if raw := c.Query("isActive"); raw != "" {
		v := raw == "true"
		where = append(where, "is_active=$1")
		args = append(args, v)
	}
	q := selectCoupon
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at DESC"
	rows, err := h.db.Pool.Query(c, q, args...)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch coupons")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		item, e := scan(rows)
		if e != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode coupons")
			return
		}
		items = append(items, item)
	}
	_ = httpx.OK(c, items, "Coupons fetched successfully")
}
func (h *Handler) Create(c *gin.Context) {
	claims := h.requireAdmin(c)
	if claims == nil {
		return
	}
	var b map[string]any
	if c.ShouldBindJSON(&b) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	for _, k := range []string{"code", "description", "type", "validFrom", "validTo"} {
		if text(b[k]) == "" {
			_ = httpx.Fail(c, 400, "invalid_request", "Missing required fields")
			return
		}
	}
	typ := text(b["type"])
	value, ok := num(b["value"])
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_request", "value is required")
		return
	}
	if typ != "percentage" && typ != "fixed" {
		_ = httpx.Fail(c, 400, "invalid_request", "type must be 'percentage' or 'fixed'")
		return
	}
	if typ == "percentage" && (value < 0 || value > 100) {
		_ = httpx.Fail(c, 400, "invalid_request", "Percentage value must be between 0 and 100")
		return
	}
	from, err := time.Parse(time.RFC3339, text(b["validFrom"]))
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "validFrom must be a valid ISO timestamp")
		return
	}
	to, err := time.Parse(time.RFC3339, text(b["validTo"]))
	if err != nil || to.Before(from) {
		_ = httpx.Fail(c, 400, "invalid_request", "validTo must be after validFrom")
		return
	}
	ids, _ := json.Marshal(array(b["applicableExperienceIds"]))
	var id string
	err = h.db.Pool.QueryRow(c, `INSERT INTO coupons(code,description,type,value,min_booking_amount,max_discount_amount,max_uses,applicable_experience_ids,valid_from,valid_to,is_active,created_by) VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, COALESCE($11,true), $12) RETURNING id::text`, strings.ToUpper(text(b["code"])), text(b["description"]), typ, value, numberOrZero(b["minBookingAmount"]), nullableNumber(b["maxDiscountAmount"]), int(numberOrZero(b["maxUses"])), ids, from, to, nullableBool(b["isActive"]), claims.UserID).Scan(&id)
	if err != nil {
		_ = httpx.Fail(c, 409, "conflict", "Coupon code already exists or is invalid")
		return
	}
	item, _ := h.get(c, id)
	_ = httpx.Created(c, item, "Coupon created successfully")
}
func (h *Handler) Validate(c *gin.Context) {
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
	code := strings.ToUpper(text(b["code"]))
	amount, ok := num(b["bookingAmount"])
	if code == "" || !ok {
		_ = httpx.Fail(c, 400, "invalid_request", "Missing required fields: code, bookingAmount")
		return
	}
	var id, typ string
	var value, min, max float64
	var maxUses, used int
	var active bool
	var from, to time.Time
	var ids, usages []byte
	err := h.db.Pool.QueryRow(c, "SELECT id::text,type,value,min_booking_amount,COALESCE(max_discount_amount,0),max_uses,used_count,is_active,valid_from,valid_to,applicable_experience_ids,usages FROM coupons WHERE code=$1", code).Scan(&id, &typ, &value, &min, &max, &maxUses, &used, &active, &from, &to, &ids, &usages)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.OK(c, gin.H{"valid": false, "reason": "Coupon not found"}, "Validation result")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to validate coupon")
		return
	}
	fail := func(reason string) { _ = httpx.OK(c, gin.H{"valid": false, "reason": reason}, "Validation result") }
	if !active {
		fail("Coupon is inactive")
		return
	}
	now := time.Now()
	if now.Before(from) {
		fail("Coupon is not yet valid")
		return
	}
	if now.After(to) {
		fail("Coupon has expired")
		return
	}
	if maxUses != 0 && used >= maxUses {
		fail("Coupon usage limit reached")
		return
	}
	if amount < min {
		fail("Minimum booking amount required")
		return
	}
	var applicable []string
	_ = json.Unmarshal(ids, &applicable)
	if len(applicable) > 0 && !contains(applicable, text(b["experienceId"])) {
		fail("Coupon is not applicable to this experience")
		return
	}
	var usedBy []map[string]any
	_ = json.Unmarshal(usages, &usedBy)
	for _, u := range usedBy {
		if text(u["userId"]) == claims.UserID {
			fail("You have already used this coupon")
			return
		}
	}
	discount := value
	if typ == "percentage" {
		discount = amount * value / 100
		if max > 0 && discount > max {
			discount = max
		}
	} else if discount > amount {
		discount = amount
	}
	discount = math.Round(discount*100) / 100
	_ = id
	_ = httpx.OK(c, gin.H{"valid": true, "discountAmount": discount, "finalAmount": math.Round((amount-discount)*100) / 100, "couponCode": code, "type": typ, "value": value}, "Coupon is valid")
}
func (h *Handler) Get(c *gin.Context) {
	if h.requireAdmin(c) == nil {
		return
	}
	item, err := h.get(c, c.Param("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Coupon not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	_ = httpx.OK(c, item, "Coupon fetched successfully")
}
func (h *Handler) Update(c *gin.Context) {
	if h.requireAdmin(c) == nil {
		return
	}
	var b map[string]any
	if c.ShouldBindJSON(&b) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	sets := []string{}
	args := []any{}
	if v, ok := b["code"]; ok {
		sets = append(sets, "code=$"+itoa(len(args)+1))
		args = append(args, strings.ToUpper(text(v)))
	}
	for _, field := range []struct{ k, col string }{{"description", "description"}, {"type", "type"}, {"isActive", "is_active"}} {
		if v, ok := b[field.k]; ok {
			sets = append(sets, field.col+"=$"+itoa(len(args)+1))
			args = append(args, v)
		}
	}
	for _, field := range []struct{ k, col string }{{"value", "value"}, {"minBookingAmount", "min_booking_amount"}, {"maxDiscountAmount", "max_discount_amount"}, {"maxUses", "max_uses"}} {
		if v, ok := b[field.k]; ok {
			sets = append(sets, field.col+"=$"+itoa(len(args)+1))
			args = append(args, numberOrZero(v))
		}
	}
	for _, field := range []string{"validFrom", "validTo"} {
		if v, ok := b[field]; ok {
			parsed, e := time.Parse(time.RFC3339, text(v))
			if e != nil {
				_ = httpx.Fail(c, 400, "invalid_request", "Invalid coupon date")
				return
			}
			sets = append(sets, map[string]string{"validFrom": "valid_from", "validTo": "valid_to"}[field]+"=$"+itoa(len(args)+1))
			args = append(args, parsed)
		}
	}
	if len(sets) == 0 {
		_ = httpx.Fail(c, 400, "invalid_request", "No update fields supplied")
		return
	}
	args = append(args, c.Param("id"))
	var id string
	if err := h.db.Pool.QueryRow(c, "UPDATE coupons SET "+strings.Join(sets, ",")+",updated_at=now() WHERE id=$"+itoa(len(args))+"::uuid RETURNING id::text", args...).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Coupon not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid coupon update")
		return
	}
	item, _ := h.get(c, id)
	_ = httpx.OK(c, item, "Coupon updated successfully")
}
func (h *Handler) Delete(c *gin.Context) {
	if h.requireAdmin(c) == nil {
		return
	}
	var id string
	if err := h.db.Pool.QueryRow(c, "UPDATE coupons SET is_active=false,updated_at=now() WHERE id=$1::uuid RETURNING id::text", c.Param("id")).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Coupon not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid coupon ID")
		return
	}
	item, _ := h.get(c, id)
	_ = httpx.OK(c, item, "Coupon deactivated successfully")
}
func (h *Handler) requireAdmin(c *gin.Context) *middleware.Claims {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return nil
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return nil
	}
	return claims
}
func (h *Handler) admin(c *gin.Context) bool { return h.requireAdmin(c) != nil }
func (h *Handler) get(c *gin.Context, id string) (map[string]any, error) {
	return scan(h.db.Pool.QueryRow(c, selectCoupon+" WHERE id=$1::uuid", id))
}
func scan(row pgx.Row) (map[string]any, error) {
	var id, code, description, typ, from, to string
	var value, min, max float64
	var maxUses, used int
	var ids, usages []byte
	var active bool
	var createdBy, created, updated string
	if err := row.Scan(&id, &code, &description, &typ, &value, &min, &max, &maxUses, &used, &ids, &from, &to, &active, &usages, &createdBy, &created, &updated); err != nil {
		return nil, err
	}
	return map[string]any{"_id": id, "code": code, "description": description, "type": typ, "value": value, "minBookingAmount": min, "maxDiscountAmount": max, "maxUses": maxUses, "usedCount": used, "applicableExperienceIds": arrayDecoded(ids), "validFrom": from, "validTo": to, "isActive": active, "usages": arrayDecoded(usages), "createdBy": createdBy, "createdAt": created, "updatedAt": updated}, nil
}
func text(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }
func num(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}
func numberOrZero(v any) float64 { n, _ := num(v); return n }
func nullableNumber(v any) any {
	if v == nil {
		return nil
	}
	return numberOrZero(v)
}
func nullableBool(v any) any {
	if v == nil {
		return nil
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return nil
}
func array(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}
func arrayDecoded(b []byte) any {
	var v any
	if len(b) > 0 && json.Unmarshal(b, &v) == nil {
		return v
	}
	return []any{}
}
func contains(a []string, v string) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}
func itoa(v int) string { return fmt.Sprint(v) }
