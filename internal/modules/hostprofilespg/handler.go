package hostprofilespg

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/platform/database"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

type Handler struct{ db *database.Postgres }

func NewHandler(db *database.Postgres) *Handler { return &Handler{db: db} }

const selectProfile = `SELECT id::text,user_id::text,display_name,host_type,description,phone,address,kyc,bank_details,documents,safety_checklist,agreement_accepted,verification_status,onboarding_step,onboarding_completed,profile_meta,created_at::text,updated_at::text FROM host_profiles`

func (h *Handler) List(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	q := selectProfile
	args := []any{}
	if admin(claims) {
		if status := text(c.Query("status")); status != "" {
			q += " WHERE verification_status=$1"
			args = append(args, status)
		}
	} else {
		if claims.Role != "Host" {
			_ = httpx.Fail(c, 403, "forbidden", "Host access required")
			return
		}
		q += " WHERE user_id=$1::uuid"
		args = append(args, claims.UserID)
	}
	q += " ORDER BY created_at DESC"
	rows, err := h.db.Pool.Query(c, q, args...)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch host profiles")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		item, e := scan(rows, admin(claims))
		if e != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode host profiles")
			return
		}
		items = append(items, item)
	}
	_ = httpx.OK(c, items, "")
}
func (h *Handler) Create(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || (!admin(claims) && claims.Role != "Host") {
		_ = httpx.Fail(c, 403, "forbidden", "Host access required")
		return
	}
	var b map[string]any
	if c.ShouldBindJSON(&b) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	for _, k := range []string{"displayName", "hostType", "phone"} {
		if text(b[k]) == "" {
			_ = httpx.Fail(c, 400, "invalid_request", k+" is required")
			return
		}
	}
	var exists bool
	if err := h.db.Pool.QueryRow(c, "SELECT EXISTS(SELECT 1 FROM host_profiles WHERE user_id=$1::uuid)", claims.UserID).Scan(&exists); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to check host profile")
		return
	}
	if exists {
		_ = httpx.Fail(c, 409, "conflict", "A host profile already exists for this user")
		return
	}
	meta, _ := json.Marshal(map[string]any{"altPhone": b["altPhone"], "website": b["website"]})
	checklist, _ := json.Marshal(defaultChecklist())
	var id string
	err := h.db.Pool.QueryRow(c, `INSERT INTO host_profiles(user_id,display_name,host_type,description,phone,safety_checklist,documents,profile_meta) VALUES($1::uuid,$2,$3,$4,$5,$6,'[]'::jsonb,$7) RETURNING id::text`, claims.UserID, text(b["displayName"]), text(b["hostType"]), text(b["description"]), text(b["phone"]), checklist, meta).Scan(&id)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to create host profile")
		return
	}
	item, _ := h.get(c, id, admin(claims))
	_ = httpx.Created(c, item, "Host profile created")
}
func (h *Handler) Get(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	item, err := h.get(c, c.Param("id"), admin(claims))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Host profile not found")
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
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	item, err := h.get(c, c.Param("id"), admin(claims))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Host profile not found")
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
	var b map[string]any
	if c.ShouldBindJSON(&b) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	sets := []string{}
	args := []any{}
	for _, f := range []struct{ k, col string }{{"displayName", "display_name"}, {"hostType", "host_type"}, {"description", "description"}, {"phone", "phone"}} {
		if v, ok := b[f.k]; ok {
			sets = append(sets, f.col+"=$"+itoa(len(args)+1))
			args = append(args, text(v))
		}
	}
	if len(sets) == 0 {
		_ = httpx.OK(c, item, "Host profile updated")
		return
	}
	args = append(args, c.Param("id"))
	if _, err := h.db.Pool.Exec(c, "UPDATE host_profiles SET "+strings.Join(sets, ",")+",updated_at=now() WHERE id=$"+itoa(len(args))+"::uuid", args...); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update host profile")
		return
	}
	updated, _ := h.get(c, c.Param("id"), admin(claims))
	_ = httpx.OK(c, updated, "Host profile updated")
}
func (h *Handler) Action(c *gin.Context)                { h.action(c, c.Param("action")) }
func (h *Handler) ActionNamed(c *gin.Context, a string) { h.action(c, a) }
func (h *Handler) action(c *gin.Context, action string) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	item, err := h.get(c, c.Param("id"), admin(claims))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Host profile not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	if action == "status" || action == "documentsReview" {
		if !admin(claims) {
			_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
			return
		}
	} else if !admin(claims) && item["userId"] != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var b map[string]any
	_ = c.ShouldBindJSON(&b)
	if b == nil {
		b = map[string]any{}
	}
	if action == "submitForReview" {
		missing := []string{}
		if emptyObject(item["kyc"]) {
			missing = append(missing, "kyc")
		}
		if emptyObject(item["address"]) {
			missing = append(missing, "address")
		}
		if emptyObject(item["bankDetails"]) {
			missing = append(missing, "bankDetails")
		}
		docs, _ := item["documents"].([]any)
		if len(docs) < 2 {
			missing = append(missing, "documents (minimum 2 required)")
		}
		if item["agreementAccepted"] != true {
			missing = append(missing, "agreementAccepted")
		}
		if len(missing) > 0 {
			_ = httpx.Fail(c, 400, "incomplete_onboarding", "Incomplete onboarding")
			return
		}
		if _, err := h.db.Pool.Exec(c, "UPDATE host_profiles SET verification_status='under_review',onboarding_completed=true,onboarding_step=6,updated_at=now() WHERE id=$1::uuid", c.Param("id")); err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to submit for review")
			return
		}
		updated, _ := h.get(c, c.Param("id"), false)
		_ = httpx.OK(c, updated, "Profile submitted for review")
		return
	}
	if action == "documents" {
		docs := item["documents"]
		if c.Request.Method == "GET" {
			_ = httpx.OK(c, docs, "")
			return
		}
		if text(b["type"]) == "" || text(b["label"]) == "" || text(b["url"]) == "" {
			_ = httpx.Fail(c, 400, "invalid_request", "type, label, and url are required")
			return
		}
		arr, _ := docs.([]any)
		doc := map[string]any{"_id": uuid.NewString(), "type": text(b["type"]), "label": text(b["label"]), "url": text(b["url"]), "mimeType": text(b["mimeType"]), "status": "pending", "uploadedAt": time.Now().UTC()}
		arr = append(arr, doc)
		raw, _ := json.Marshal(arr)
		if _, err := h.db.Pool.Exec(c, "UPDATE host_profiles SET documents=$1::jsonb,onboarding_step=GREATEST(onboarding_step,5),updated_at=now() WHERE id=$2::uuid", raw, c.Param("id")); err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to upload document")
			return
		}
		_ = httpx.Created(c, arr, "Document uploaded")
		return
	}
	if action == "documentsReview" {
		docID := text(b["docId"])
		if docID == "" {
			docID = c.Param("docId")
		}
		status := text(b["status"])
		if !allowed(status, "approved", "rejected", "pending") {
			_ = httpx.Fail(c, 400, "invalid_request", "Invalid document status")
			return
		}
		docs, _ := item["documents"].([]any)
		found := false
		for _, raw := range docs {
			doc, ok := raw.(map[string]any)
			if !ok || text(doc["_id"]) != docID {
				continue
			}
			doc["status"] = status
			doc["reviewedBy"] = claims.UserID
			doc["reviewedAt"] = time.Now().UTC()
			if reason := text(b["rejectionReason"]); reason != "" {
				doc["rejectionReason"] = reason
			}
			found = true
		}
		if !found {
			_ = httpx.Fail(c, 404, "not_found", "Document not found")
			return
		}
		raw, _ := json.Marshal(docs)
		if _, err := h.db.Pool.Exec(c, "UPDATE host_profiles SET documents=$1::jsonb,updated_at=now() WHERE id=$2::uuid", raw, c.Param("id")); err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to review document")
			return
		}
		updated, _ := h.get(c, c.Param("id"), true)
		_ = httpx.OK(c, updated, "Document reviewed")
		return
	}
	field := map[string]string{"address": "address", "kyc": "kyc", "bankDetails": "bank_details", "safetyChecklist": "safety_checklist"}[action]
	if action == "agreement" {
		version := text(b["version"])
		if version == "" {
			version = "v1.0"
		}
		if _, err := h.db.Pool.Exec(c, "UPDATE host_profiles SET agreement_accepted=true,profile_meta=jsonb_set(jsonb_set(profile_meta,'{agreementVersion}',$1::jsonb,true),'{agreementAcceptedAt}',to_jsonb(now()),true),updated_at=now() WHERE id=$2::uuid", jsonString(version), c.Param("id")); err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to record agreement")
			return
		}
	} else if action == "status" {
		status := text(b["status"])
		if !allowed(status, "pending", "under_review", "verified", "suspended", "rejected") {
			_ = httpx.Fail(c, 400, "invalid_request", "Invalid verification status")
			return
		}
		if _, err := h.db.Pool.Exec(c, "UPDATE host_profiles SET verification_status=$1,updated_at=now() WHERE id=$2::uuid", status, c.Param("id")); err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to update verification status")
			return
		}
	} else if field != "" {
		raw, _ := json.Marshal(b)
		if field == "safety_checklist" {
			var current []byte
			_ = h.db.Pool.QueryRow(c, "SELECT COALESCE(safety_checklist,'[]'::jsonb) FROM host_profiles WHERE id=$1::uuid", c.Param("id")).Scan(&current)
			raw = mergeChecklist(current, b)
		}
		step := 0
		if field == "kyc" {
			step = 2
		}
		if field == "address" {
			step = 3
		}
		if field == "bank_details" {
			step = 4
		}
		if field == "safety_checklist" {
			step = 5
		}
		if _, err := h.db.Pool.Exec(c, "UPDATE host_profiles SET "+field+"=$1::jsonb,onboarding_step=GREATEST(onboarding_step,$2),updated_at=now() WHERE id=$3::uuid", raw, step, c.Param("id")); err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to update host profile")
			return
		}
	}
	updated, _ := h.get(c, c.Param("id"), admin(claims))
	_ = httpx.OK(c, updated, "Host profile updated")
}

func (h *Handler) get(c *gin.Context, id string, showSensitive bool) (map[string]any, error) {
	return scan(h.db.Pool.QueryRow(c, selectProfile+" WHERE id=$1::uuid", id), showSensitive)
}
func scan(row pgx.Row, show bool) (map[string]any, error) {
	var id, user, display, typ, description, phone string
	var address, kyc, bank, docs, safety, meta []byte
	var agreement bool
	var status string
	var step int
	var completed bool
	var created, updated string
	if err := row.Scan(&id, &user, &display, &typ, &description, &phone, &address, &kyc, &bank, &docs, &safety, &agreement, &status, &step, &completed, &meta, &created, &updated); err != nil {
		return nil, err
	}
	item := map[string]any{"_id": id, "userId": user, "displayName": display, "hostType": typ, "description": description, "phone": phone, "address": decode(address), "kyc": decode(kyc), "bankDetails": decode(bank), "documents": decode(docs), "safetyChecklist": decode(safety), "agreementAccepted": agreement, "verificationStatus": status, "onboardingStep": step, "onboardingCompleted": completed, "createdAt": created, "updatedAt": updated}
	var extra map[string]any
	_ = json.Unmarshal(meta, &extra)
	for k, v := range extra {
		item[k] = v
	}
	if !show {
		if m, ok := item["bankDetails"].(map[string]any); ok {
			if n, ok := m["accountNumber"].(string); ok && len(n) > 4 {
				m["accountNumber"] = "****" + n[len(n)-4:]
			}
		}
	}
	return item, nil
}
func admin(c *middleware.Claims) bool {
	return c != nil && (c.Role == "Admin" || c.Role == "SuperAdmin")
}
func text(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }
func decode(b []byte) any {
	var v any
	if len(b) > 0 && json.Unmarshal(b, &v) == nil {
		return v
	}
	return map[string]any{}
}
func defaultChecklist() []map[string]any {
	out := make([]map[string]any, 8)
	for i := range out {
		out[i] = map[string]any{"key": "item" + itoa(i+1), "isCompleted": false}
	}
	return out
}
func allowed(v string, a ...string) bool {
	for _, x := range a {
		if v == x {
			return true
		}
	}
	return false
}
func itoa(v int) string          { return fmt.Sprint(v) }
func jsonString(v string) string { b, _ := json.Marshal(v); return string(b) }
func emptyObject(v any) bool     { m, ok := v.(map[string]any); return !ok || len(m) == 0 }

// mergeChecklist keeps the safety checklist an array of {key,isCompleted}.
// The body may carry a full "items" array (mobile) or a single {key,isCompleted} toggle (web).
func mergeChecklist(current []byte, body map[string]any) []byte {
	var list []map[string]any
	_ = json.Unmarshal(current, &list)
	set := func(key string, done bool) {
		for _, it := range list {
			if text(it["key"]) == key {
				it["isCompleted"] = done
				return
			}
		}
		list = append(list, map[string]any{"key": key, "isCompleted": done})
	}
	if items, ok := body["items"].([]any); ok {
		for _, raw := range items {
			if it, ok := raw.(map[string]any); ok && text(it["key"]) != "" {
				done, _ := it["isCompleted"].(bool)
				set(text(it["key"]), done)
			}
		}
	} else if key := text(body["key"]); key != "" {
		done, _ := body["isCompleted"].(bool)
		set(key, done)
	}
	out, err := json.Marshal(list)
	if err != nil || list == nil {
		return []byte("[]")
	}
	return out
}
