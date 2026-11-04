package rewards

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

const referralSelect = `SELECT id::text,referrer_id::text,COALESCE(referee_id::text,''),referral_code,status,referrer_reward_amount,referee_discount_amount,expires_at::text,created_at::text,updated_at::text FROM referrals`

func (h *Handler) Referrals(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	rows, err := h.db.Pool.Query(c, referralSelect+" WHERE referrer_id=$1::uuid OR referee_id=$1::uuid ORDER BY created_at DESC", claims.UserID)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch referrals")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		item, e := scanReferral(rows)
		if e != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode referrals")
			return
		}
		items = append(items, item)
	}
	_ = httpx.OK(c, items, "Referrals fetched successfully")
}
func (h *Handler) CreateReferral(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var exists bool
	if err := h.db.Pool.QueryRow(c, "SELECT EXISTS(SELECT 1 FROM referrals WHERE referrer_id=$1::uuid AND status='pending')", claims.UserID).Scan(&exists); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to check referral")
		return
	}
	if exists {
		_ = httpx.Fail(c, 409, "conflict", "You already have an active referral code")
		return
	}
	var id string
	expires := time.Now().UTC().Add(30 * 24 * time.Hour)
	if err := h.db.Pool.QueryRow(c, `INSERT INTO referrals(referrer_id,referral_code,expires_at) VALUES($1::uuid,'VIH'||upper(substr(encode(gen_random_bytes(6),'hex'),1,5)),$2) RETURNING id::text`, claims.UserID, expires).Scan(&id); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to create referral")
		return
	}
	item, _ := h.referral(c, id)
	_ = httpx.Created(c, item, "Referral code created successfully")
}
func (h *Handler) Apply(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var b map[string]any
	if c.ShouldBindJSON(&b) != nil || strings.TrimSpace(text(b["referralCode"])) == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "Missing required field: referralCode")
		return
	}
	tx, err := h.db.Pool.Begin(c)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not start referral transaction")
		return
	}
	defer tx.Rollback(c)
	var id, referrer string
	var discount float64
	if err := tx.QueryRow(c, "SELECT id::text,referrer_id::text,referee_discount_amount FROM referrals WHERE referral_code=$1 AND status='pending' AND expires_at>now() FOR UPDATE", strings.ToUpper(text(b["referralCode"]))).Scan(&id, &referrer, &discount); errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Referral code not found, already used, or expired")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to apply referral")
		return
	}
	if referrer == claims.UserID {
		_ = httpx.Fail(c, 400, "invalid_request", "You cannot use your own referral code")
		return
	}
	if _, err := tx.Exec(c, "UPDATE referrals SET referee_id=$1::uuid,status='completed',updated_at=now() WHERE id=$2::uuid", claims.UserID, id); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to apply referral code")
		return
	}
	if err := tx.Commit(c); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not commit referral")
		return
	}
	item, _ := h.referral(c, id)
	_ = httpx.OK(c, gin.H{"referral": item, "discountAmount": discount, "message": fmt.Sprintf("You will receive a ₹%.0f discount on your first booking!", discount)}, "Referral code applied successfully")
}

func (h *Handler) Loyalty(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	if err := h.ensureLoyalty(c, claims); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to load loyalty profile")
		return
	}
	item, err := h.loyalty(c, claims.UserID)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch loyalty profile")
		return
	}
	_ = httpx.OK(c, gin.H{"loyalty": item}, "Loyalty profile fetched successfully")
}
func (h *Handler) Earn(c *gin.Context) {
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
	typ := text(b["type"])
	rates := map[string]int{"booking": 100, "review": 50, "referral": 200, "trip_completed": 150, "repeat_travel": 75}
	xp, ok := rates[typ]
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid XP type")
		return
	}
	if err := h.ensureLoyalty(c, claims); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to initialize loyalty profile")
		return
	}
	tx, err := h.db.Pool.Begin(c)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not start loyalty transaction")
		return
	}
	defer tx.Rollback(c)
	entry := map[string]any{"type": typ, "xp": xp, "description": text(b["description"]), "referenceId": text(b["referenceId"]), "createdAt": time.Now().UTC()}
	raw, _ := json.Marshal([]any{entry})
	var total int
	if err := tx.QueryRow(c, "UPDATE loyalty_profiles SET total_xp=total_xp+$1,transactions=$2::jsonb||transactions,level=CASE WHEN total_xp+$1>=4000 THEN 'Village Legend' WHEN total_xp+$1>=1500 THEN 'Cultural Insider' WHEN total_xp+$1>=500 THEN 'Wanderer' ELSE 'Explorer' END,updated_at=now() WHERE user_id=$3::uuid RETURNING total_xp", xp, raw, claims.UserID).Scan(&total); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to award XP")
		return
	}
	if err := tx.Commit(c); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not commit XP award")
		return
	}
	item, _ := h.loyalty(c, claims.UserID)
	_ = httpx.OK(c, gin.H{"loyalty": item}, fmt.Sprintf("+%d XP awarded for %s", xp, strings.ReplaceAll(typ, "_", " ")))
}

func (h *Handler) ensureLoyalty(c *gin.Context, claims *middleware.Claims) error {
	_, err := h.db.Pool.Exec(c, "INSERT INTO loyalty_profiles(user_id,user_email) VALUES($1::uuid,$2) ON CONFLICT(user_id) DO NOTHING", claims.UserID, claims.Email)
	return err
}
func (h *Handler) loyalty(c *gin.Context, id string) (map[string]any, error) {
	var email, level string
	var xp, trips, reviews, referrals int
	var spent float64
	var transactions, badges []byte
	err := h.db.Pool.QueryRow(c, "SELECT user_email,level,total_xp,transactions,badges,trips_completed,reviews_given,referrals_converted,total_spent FROM loyalty_profiles WHERE user_id=$1::uuid", id).Scan(&email, &level, &xp, &transactions, &badges, &trips, &reviews, &referrals, &spent)
	return map[string]any{"userId": id, "userEmail": email, "totalXP": xp, "level": level, "transactions": decode(transactions), "badges": decode(badges), "tripsCompleted": trips, "reviewsGiven": reviews, "referralsConverted": referrals, "totalSpent": spent}, err
}
func (h *Handler) referral(c *gin.Context, id string) (map[string]any, error) {
	var rid, referrer, referee, code, status, expires, created, updated string
	var reward, discount float64
	err := h.db.Pool.QueryRow(c, referralSelect+" WHERE id=$1::uuid", id).Scan(&rid, &referrer, &referee, &code, &status, &reward, &discount, &expires, &created, &updated)
	return map[string]any{"_id": rid, "referrerId": referrer, "refereeId": referee, "referralCode": code, "status": status, "referrerRewardAmount": reward, "refereeDiscountAmount": discount, "expiresAt": expires, "createdAt": created, "updatedAt": updated}, err
}
func scanReferral(row pgx.Row) (map[string]any, error) {
	var id, referrer, referee, code, status, expires, created, updated string
	var reward, discount float64
	err := row.Scan(&id, &referrer, &referee, &code, &status, &reward, &discount, &expires, &created, &updated)
	return map[string]any{"_id": id, "referrerId": referrer, "refereeId": referee, "referralCode": code, "status": status, "referrerRewardAmount": reward, "refereeDiscountAmount": discount, "expiresAt": expires, "createdAt": created, "updatedAt": updated}, err
}
func text(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }
func decode(b []byte) any {
	var v any
	if len(b) > 0 && json.Unmarshal(b, &v) == nil {
		return v
	}
	return []any{}
}
