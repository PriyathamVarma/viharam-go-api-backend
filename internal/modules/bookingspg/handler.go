package bookingspg

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/modules/portalcalc"
	"github.com/viharam/go-api-backend/internal/platform/database"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

type Handler struct{ db *database.Postgres }

func NewHandler(db *database.Postgres) *Handler { return &Handler{db: db} }

const bookingSelect = `SELECT b.id::text,b.traveler_id::text,b.property_id::text,b.host_id::text,b.check_in::text,b.check_out::text,b.guests,b.rooms_required,b.total_price_paise,b.price_per_night_paise,b.nights,b.status,COALESCE(b.notes,''),b.created_at::text,b.updated_at::text,COALESCE(p.name,''),COALESCE(p.city,''),COALESCE(p.address,''),COALESCE(p.image,''),COALESCE(p.images,'[]'::jsonb),COALESCE(u.name,'Traveller'),COALESCE(u.email,''),COALESCE(u.phone_number,'') FROM property_bookings b LEFT JOIN properties p ON p.id=b.property_id LEFT JOIN users u ON u.id=b.traveler_id`

func (h *Handler) List(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	where, args := []string{}, []any{}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" {
		if claims.Role == "Ambassador" {
			_ = httpx.OK(c, []map[string]any{}, "")
			return
		}
		column := "traveler_id"
		if claims.Role == "Host" {
			column = "host_id"
		}
		where = append(where, "b."+column+"=$1::uuid")
		args = append(args, claims.UserID)
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		where = append(where, fmt.Sprintf("b.status=$%d", len(args)+1))
		args = append(args, status)
	}
	query := bookingSelect
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY b.created_at DESC"
	rows, err := h.db.Pool.Query(c, query, args...)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch bookings")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		item, e := scan(rows)
		if e != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode bookings")
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
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	propertyID, checkInRaw, checkOutRaw := text(body["propertyId"]), text(body["checkIn"]), text(body["checkOut"])
	guests := int(number(body["guests"]))
	if propertyID == "" || checkInRaw == "" || checkOutRaw == "" || guests < 1 {
		_ = httpx.Fail(c, 400, "invalid_request", "propertyId, checkIn, checkOut and guests are required")
		return
	}
	checkIn, err := parseDate(checkInRaw)
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid check-in date")
		return
	}
	checkOut, err := parseDate(checkOutRaw)
	if err != nil || !checkOut.After(checkIn) {
		_ = httpx.Fail(c, 400, "invalid_request", "Check-out must be after check-in")
		return
	}
	tx, err := h.db.Pool.Begin(c)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to start booking transaction")
		return
	}
	defer tx.Rollback(c)
	var hostID string
	var price int64
	var maxGuests, rooms int
	var status string
	err = tx.QueryRow(c, "SELECT host_id::text,price_per_night_paise,max_guests,COALESCE(NULLIF(bedrooms,0),1),status FROM properties WHERE id=$1::uuid FOR UPDATE", propertyID).Scan(&hostID, &price, &maxGuests, &rooms, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Property not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid property ID")
		return
	}
	if status != "active" {
		_ = httpx.Fail(c, 400, "unavailable", "Property is not available")
		return
	}
	if guests > maxGuests {
		_ = httpx.Fail(c, 400, "invalid_request", "Guest count exceeds property capacity")
		return
	}
	guestsPerRoom := (maxGuests + rooms - 1) / rooms
	roomsRequired := (guests + guestsPerRoom - 1) / guestsPerRoom
	var overlap int
	err = tx.QueryRow(c, "SELECT COALESCE(sum(rooms_required),0) FROM property_bookings WHERE property_id=$1::uuid AND status IN ('confirmed','completed') AND check_in<$3 AND check_out>$2", propertyID, checkIn, checkOut).Scan(&overlap)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to check availability")
		return
	}
	if overlap+roomsRequired > rooms {
		_ = httpx.Fail(c, 409, "unavailable", "Not enough rooms are available for these dates")
		return
	}
	nights := int(checkOut.Sub(checkIn).Hours() / 24)
	total := int64(nights) * price
	var id string
	_, err = tx.Exec(c, `INSERT INTO property_bookings(traveler_id,property_id,host_id,check_in,check_out,guests,rooms_required,total_price_paise,price_per_night_paise,nights,status,notes) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,'confirmed',$11)`, claims.UserID, propertyID, hostID, checkIn, checkOut, guests, roomsRequired, total, price, nights, text(body["notes"]))
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to create booking")
		return
	}
	err = tx.QueryRow(c, "SELECT id::text FROM property_bookings WHERE traveler_id=$1::uuid AND property_id=$2::uuid AND check_in=$3 AND check_out=$4 ORDER BY created_at DESC LIMIT 1", claims.UserID, propertyID, checkIn, checkOut).Scan(&id)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to load created booking")
		return
	}
	if err = tx.Commit(c); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to commit booking")
		return
	}
	item, _ := h.get(c, id)
	_ = httpx.Created(c, item, "Booking created")
}

func (h *Handler) Get(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	item, err := h.get(c, c.Param("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && item["travelerId"] != claims.UserID && item["hostId"] != claims.UserID {
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
	item, err := h.get(c, c.Param("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	status := text(item["status"])
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && item["hostId"] != claims.UserID && (item["travelerId"] != claims.UserID || status != "pending") {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	next := text(body["status"])
	if next == "" {
		next = status
	}
	if _, err := h.db.Pool.Exec(c, "UPDATE property_bookings SET status=$1,notes=COALESCE($2,notes),updated_at=now() WHERE id=$3::uuid", next, text(body["notes"]), c.Param("id")); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update booking")
		return
	}
	updated, _ := h.get(c, c.Param("id"))
	_ = httpx.OK(c, updated, "Booking updated")
}

func (h *Handler) get(c *gin.Context, id string) (map[string]any, error) {
	return scan(h.db.Pool.QueryRow(c, bookingSelect+" WHERE b.id=$1::uuid", id))
}
func scan(row pgx.Row) (map[string]any, error) {
	var id, traveler, property, host, checkIn, checkOut, status, notes, created, updated string
	var title, city, address, image, travelerName, travelerEmail, travelerPhone string
	var images []byte
	var guests, roomsRequired, nights int
	var total, price int64
	if err := row.Scan(&id, &traveler, &property, &host, &checkIn, &checkOut, &guests, &roomsRequired, &total, &price, &nights, &status, &notes, &created, &updated, &title, &city, &address, &image, &images, &travelerName, &travelerEmail, &travelerPhone); err != nil {
		return nil, err
	}
	photo := image
	if photo == "" {
		var decoded any
		if json.Unmarshal(images, &decoded) == nil {
			photo = portalcalc.FirstPhoto(decoded)
		}
	}
	return map[string]any{"_id": id, "travelerId": traveler, "propertyId": property, "hostId": host, "checkIn": checkIn, "checkOut": checkOut, "guests": guests, "roomsRequired": roomsRequired, "totalPrice": float64(total) / 100, "pricePerNight": float64(price) / 100, "nights": nights, "status": status, "notes": notes, "createdAt": created, "updatedAt": updated,
		"title": title, "experienceTitle": title, "travelerName": travelerName, "travelerEmail": travelerEmail, "travelerPhone": travelerPhone, "date": checkIn, "startTime": "", "meetingPoint": address, "city": city, "photo": photo, "guestCount": guests}, nil
}
func parseDate(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	return time.Parse("2006-01-02", v)
}
func text(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }
func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}
func positive(v string, fallback int) int {
	n, e := strconv.Atoi(v)
	if e != nil || n < 1 {
		return fallback
	}
	return n
}
