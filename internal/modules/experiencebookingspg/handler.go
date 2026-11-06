package experiencebookingspg

import (
	"encoding/json"
	"errors"
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

// Slots handles GET /experience-slots. experienceId is required; month
// (YYYY-MM) optionally limits the result to that calendar month. The slots
// are returned as {slots, total}, the shape the web app reads.
func (h *Handler) Slots(c *gin.Context) {
	q := `SELECT s.id::text,s.experience_id::text,s.date::text,s.start_time::text,s.end_time::text,s.capacity,s.booked_count,s.locked_count,s.is_blocked
FROM experience_slots s WHERE s.experience_id=$1::uuid`
	args := []any{c.Query("experienceId")}
	if month := c.Query("month"); month != "" {
		start, end, ok := portalcalc.MonthBounds(month)
		if !ok {
			_ = httpx.Fail(c, 400, "invalid_request", "month must be YYYY-MM")
			return
		}
		q += " AND s.date>=$2::date AND s.date<$3::date"
		args = append(args, start, end)
	}
	q += " ORDER BY s.date,s.start_time"
	rows, err := h.db.Pool.Query(c, q, args...)
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "experienceId is required")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, experience, date, start, end string
		var capacity, booked, locked int
		var blocked bool
		if err := rows.Scan(&id, &experience, &date, &start, &end, &capacity, &booked, &locked, &blocked); err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode slots")
			return
		}
		items = append(items, map[string]any{"_id": id, "experienceId": experience, "date": date, "startTime": start, "endTime": end, "capacity": capacity, "bookedCount": booked, "lockedCount": locked, "isBlocked": blocked, "availableSpots": capacity - booked - locked})
	}
	_ = httpx.OK(c, gin.H{"slots": items, "total": len(items)}, "")
}

func (h *Handler) SaveSlot(c *gin.Context)   { h.writeSlot(c, false) }
func (h *Handler) UpdateSlot(c *gin.Context) { h.writeSlot(c, true) }

func (h *Handler) writeSlot(c *gin.Context, update bool) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	if claims.Role != "Host" && claims.Role != "Admin" && claims.Role != "SuperAdmin" {
		_ = httpx.Fail(c, 403, "forbidden", "Only hosts can manage experience slots")
		return
	}
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	experience, date, start, end := text(body["experienceId"]), text(body["date"]), text(body["startTime"]), text(body["endTime"])
	capacity := int(number(body["capacity"]))
	if experience == "" {
		experience = text(body["experience_id"])
	}
	if capacity < 1 {
		_ = httpx.Fail(c, 400, "invalid_request", "capacity must be positive")
		return
	}
	var id string
	var err error
	if update {
		id = c.Param("id")
		_, err = h.db.Pool.Exec(c, `UPDATE experience_slots SET date=COALESCE(NULLIF($1,'')::date,date),start_time=COALESCE(NULLIF($2,'')::time,start_time),end_time=COALESCE(NULLIF($3,'')::time,end_time),capacity=$4,is_blocked=COALESCE($5,is_blocked),updated_at=now() WHERE id=$6::uuid`, date, start, end, capacity, nullableBool(body["isBlocked"]), id)
	} else {
		if experience == "" || date == "" || start == "" || end == "" {
			_ = httpx.Fail(c, 400, "invalid_request", "experienceId, date, startTime and endTime are required")
			return
		}
		err = h.db.Pool.QueryRow(c, `INSERT INTO experience_slots(experience_id,date,start_time,end_time,capacity,is_blocked) VALUES($1::uuid,$2,$3,$4,$5,COALESCE($6,false)) RETURNING id::text`, experience, date, start, end, capacity, nullableBool(body["isBlocked"])).Scan(&id)
	}
	if err != nil {
		_ = httpx.Fail(c, 409, "slot_error", "Could not save experience slot")
		return
	}
	_ = claims
	_ = httpx.OK(c, map[string]any{"_id": id}, "Experience slot saved")
}

func (h *Handler) DeleteSlot(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	if claims.Role != "Host" && claims.Role != "Admin" && claims.Role != "SuperAdmin" {
		_ = httpx.Fail(c, 403, "forbidden", "Only hosts can manage experience slots")
		return
	}
	if _, err := h.db.Pool.Exec(c, "DELETE FROM experience_slots WHERE id=$1::uuid", c.Param("id")); err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Could not delete slot")
		return
	}
	_ = httpx.OK(c, nil, "Experience slot deleted")
}

func (h *Handler) CreateLock(c *gin.Context) {
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
	slot, guests := text(body["slotId"]), int(number(body["guests"]))
	if slot == "" || guests < 1 {
		_ = httpx.Fail(c, 400, "invalid_request", "slotId and guests are required")
		return
	}
	tx, err := h.db.Pool.Begin(c)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not start lock transaction")
		return
	}
	defer tx.Rollback(c)
	var capacity, booked, locked int
	if err := tx.QueryRow(c, "SELECT capacity,booked_count,locked_count FROM experience_slots WHERE id=$1::uuid AND is_blocked=false FOR UPDATE", slot).Scan(&capacity, &booked, &locked); errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Experience slot not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid slot ID")
		return
	}
	if guests > capacity-booked-locked {
		_ = httpx.Fail(c, 409, "unavailable", "Slot does not have enough capacity")
		return
	}
	var lockID string
	if err := tx.QueryRow(c, `INSERT INTO booking_locks(experience_id,slot_id,traveler_id,guests,expires_at) SELECT experience_id,id,$1::uuid,$2,now()+interval '10 minutes' FROM experience_slots WHERE id=$3::uuid RETURNING lock_id::text`, claims.UserID, guests, slot).Scan(&lockID); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not create booking lock")
		return
	}
	if _, err := tx.Exec(c, "UPDATE experience_slots SET locked_count=locked_count+$1,updated_at=now() WHERE id=$2::uuid", guests, slot); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not reserve slot lock")
		return
	}
	if err := tx.Commit(c); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not commit booking lock")
		return
	}
	_ = httpx.Created(c, gin.H{"lockId": lockID, "slotId": slot, "guests": guests, "expiresAt": time.Now().UTC().Add(10 * time.Minute)}, "Booking lock created")
}

func (h *Handler) ReleaseLock(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	tx, err := h.db.Pool.Begin(c)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not start lock transaction")
		return
	}
	defer tx.Rollback(c)
	var slot string
	var guests int
	err = tx.QueryRow(c, "DELETE FROM booking_locks WHERE lock_id=$1::uuid AND (traveler_id=$2::uuid OR $3 IN ('Admin','SuperAdmin')) RETURNING slot_id::text,guests", c.Param("lockId"), claims.UserID, claims.Role).Scan(&slot, &guests)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Booking lock not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid lock ID")
		return
	}
	if _, err := tx.Exec(c, "UPDATE experience_slots SET locked_count=GREATEST(0,locked_count-$1),updated_at=now() WHERE id=$2::uuid", guests, slot); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not release slot lock")
		return
	}
	if err := tx.Commit(c); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not commit lock release")
		return
	}
	_ = httpx.OK(c, nil, "Booking lock released")
}

func (h *Handler) List(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	q := bookingDetailSelect
	args := []any{}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" {
		q += " WHERE b.traveler_id=$1::uuid"
		args = append(args, claims.UserID)
	}
	q += " ORDER BY b.created_at DESC"
	rows, err := h.db.Pool.Query(c, q, args...)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch experience bookings")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		item, e := scanBookingDetail(rows)
		if e != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode experience bookings")
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
	if c.ShouldBindJSON(&body) != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	slot, guests := text(body["slotId"]), int(number(body["guests"]))
	if slot == "" || guests < 1 {
		_ = httpx.Fail(c, 400, "invalid_request", "slotId and guests are required")
		return
	}
	tx, err := h.db.Pool.Begin(c)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not start booking")
		return
	}
	defer tx.Rollback(c)
	var price int64
	var expID, name, email, phone string
	var max, capacity, booked, locked int
	err = tx.QueryRow(c, `SELECT s.experience_id::text,e.price_per_person_paise,e.max_group_size,s.capacity,s.booked_count,s.locked_count,u.name,u.email,COALESCE(u.phone_number,'') FROM experience_slots s JOIN experiences e ON e.id=s.experience_id JOIN users u ON u.id=$2::uuid WHERE s.id=$1::uuid AND e.status='published' FOR UPDATE`, slot, claims.UserID).Scan(&expID, &price, &max, &capacity, &booked, &locked, &name, &email, &phone)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Experience slot not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid slot ID")
		return
	}
	if guests > max || guests > capacity-booked-locked {
		_ = httpx.Fail(c, 409, "unavailable", "Slot does not have enough capacity")
		return
	}
	var id, ref string
	subtotal := price * int64(guests)
	total := subtotal
	err = tx.QueryRow(c, `INSERT INTO experience_bookings(booking_ref,experience_id,slot_id,traveler_id,traveler_name,traveler_email,traveler_phone,emergency_contact,special_requests,guests,price_per_person_paise,subtotal_paise,service_fee_paise,total_paise,status,payment_status) VALUES('VHM-'||upper(substr(encode(gen_random_bytes(6),'hex'),1,10)),$1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,0,$12,'pending_payment','pending') RETURNING id::text,booking_ref`, expID, slot, claims.UserID, name, email, phone, text(body["emergencyContact"]), text(body["specialRequests"]), guests, price, subtotal, total).Scan(&id, &ref)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to create experience booking")
		return
	}
	if _, err = tx.Exec(c, `UPDATE experience_slots SET booked_count=booked_count+$1,updated_at=now() WHERE id=$2::uuid`, guests, slot); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to reserve slot")
		return
	}
	if err = tx.Commit(c); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to commit booking")
		return
	}
	_ = httpx.Created(c, map[string]any{"_id": id, "bookingRef": ref, "experienceId": expID, "slotId": slot, "guests": guests, "total": float64(total) / 100, "status": "pending_payment"}, "Experience booking created")
}

func (h *Handler) Get(c *gin.Context) { h.getWith(c, c.Param("id")) }
func (h *Handler) getWith(c *gin.Context, id string) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	item, err := scanBookingDetail(h.db.Pool.QueryRow(c, bookingDetailSelect+" WHERE b.id=$1::uuid", id))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Experience booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && item["travelerId"] != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	_ = httpx.OK(c, item, "")
}

// Cancel handles POST /experience-bookings/:id/cancel for the booking's
// traveller or an admin. It stores the optional reason and the cancellation
// time and gives the booked seats back to the slot. Cancelling an already
// cancelled booking is a no-op; completed or refunded bookings return 409.
// No refund is calculated or recorded here.
func (h *Handler) Cancel(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil {
		body = map[string]any{}
	}
	tx, err := h.db.Pool.Begin(c)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not start cancellation")
		return
	}
	defer tx.Rollback(c)
	var traveler, slot, status string
	var guests int
	var cancelledAt *time.Time
	err = tx.QueryRow(c, "SELECT traveler_id::text,slot_id::text,guests,status,cancelled_at FROM experience_bookings WHERE id=$1::uuid FOR UPDATE", c.Param("id")).Scan(&traveler, &slot, &guests, &status, &cancelledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Experience booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && traveler != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	switch status {
	case "cancelled":
		_ = httpx.OK(c, cancelResponse(cancelledAt), "Booking updated")
		return
	case "completed", "refunded":
		_ = httpx.Fail(c, 409, "invalid_state", "A "+status+" booking cannot be cancelled")
		return
	}
	var cancelled time.Time
	if err := tx.QueryRow(c, "UPDATE experience_bookings SET status='cancelled',cancellation_reason=NULLIF($1,''),cancelled_at=now(),updated_at=now() WHERE id=$2::uuid RETURNING cancelled_at", portalcalc.CancelReason(text(body["reason"])), c.Param("id")).Scan(&cancelled); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not cancel booking")
		return
	}
	if _, err := tx.Exec(c, "UPDATE experience_slots SET booked_count=GREATEST(0,booked_count-$1),updated_at=now() WHERE id=$2::uuid", guests, slot); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not release slot capacity")
		return
	}
	if err := tx.Commit(c); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not commit cancellation")
		return
	}
	_ = httpx.OK(c, cancelResponse(&cancelled), "Booking updated")
}

func cancelResponse(cancelledAt *time.Time) gin.H {
	out := gin.H{"status": "cancelled"}
	if cancelledAt != nil {
		out["cancelledAt"] = cancelledAt.UTC().Format(time.RFC3339)
	}
	return out
}

func (h *Handler) Status(c *gin.Context) {
	var b map[string]any
	_ = c.ShouldBindJSON(&b)
	next := text(b["status"])
	if next == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "status is required")
		return
	}
	h.transition(c, next)
}
func (h *Handler) Refund(c *gin.Context) { h.transition(c, "refunded") }
func (h *Handler) Reschedule(c *gin.Context) {
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
	newSlot := text(body["slotId"])
	if newSlot == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "slotId is required")
		return
	}
	tx, err := h.db.Pool.Begin(c)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not start reschedule transaction")
		return
	}
	defer tx.Rollback(c)
	var traveler, oldSlot, status string
	var guests int
	err = tx.QueryRow(c, "SELECT traveler_id::text,slot_id::text,guests,status FROM experience_bookings WHERE id=$1::uuid FOR UPDATE", c.Param("id")).Scan(&traveler, &oldSlot, &guests, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Experience booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && traveler != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	if status == "cancelled" || status == "refunded" {
		_ = httpx.Fail(c, 409, "invalid_state", "Cancelled bookings cannot be rescheduled")
		return
	}
	var newExperience string
	var capacity, booked, locked, max int
	var price int64
	err = tx.QueryRow(c, `SELECT s.experience_id::text,s.capacity,s.booked_count,s.locked_count,e.max_group_size,e.price_per_person_paise FROM experience_slots s JOIN experiences e ON e.id=s.experience_id WHERE s.id=$1::uuid AND e.status='published' AND s.is_blocked=false FOR UPDATE`, newSlot).Scan(&newExperience, &capacity, &booked, &locked, &max, &price)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Target experience slot not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid target slot ID")
		return
	}
	if guests > max || guests > capacity-booked-locked {
		_ = httpx.Fail(c, 409, "unavailable", "Target slot does not have enough capacity")
		return
	}
	if _, err := tx.Exec(c, "UPDATE experience_slots SET booked_count=GREATEST(0,booked_count-$1),updated_at=now() WHERE id=$2::uuid", guests, oldSlot); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not release old slot")
		return
	}
	if _, err := tx.Exec(c, "UPDATE experience_slots SET booked_count=booked_count+$1,updated_at=now() WHERE id=$2::uuid", guests, newSlot); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not reserve target slot")
		return
	}
	total := price * int64(guests)
	if _, err := tx.Exec(c, "UPDATE experience_bookings SET experience_id=$1::uuid,slot_id=$2::uuid,price_per_person_paise=$3,subtotal_paise=$4,total_paise=$4,rescheduled_at=now(),original_slot_id=COALESCE(original_slot_id,$5::uuid),updated_at=now() WHERE id=$6::uuid", newExperience, newSlot, price, total, oldSlot, c.Param("id")); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not update booking")
		return
	}
	if err := tx.Commit(c); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not commit reschedule")
		return
	}
	_ = httpx.OK(c, gin.H{"_id": c.Param("id"), "slotId": newSlot, "experienceId": newExperience, "guests": guests, "total": float64(total) / 100}, "Booking rescheduled")
}
func (h *Handler) transition(c *gin.Context, status string) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var traveler string
	if err := h.db.Pool.QueryRow(c, "SELECT traveler_id::text FROM experience_bookings WHERE id=$1::uuid", c.Param("id")).Scan(&traveler); errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Experience booking not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && traveler != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	if _, err := h.db.Pool.Exec(c, "UPDATE experience_bookings SET status=$1, cancelled_at=CASE WHEN $1='cancelled' THEN now() ELSE cancelled_at END, updated_at=now() WHERE id=$2::uuid", status, c.Param("id")); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Could not update booking")
		return
	}
	_ = httpx.OK(c, gin.H{"status": status}, "Booking updated")
}

// bookingDetailSelect joins the experience and slot so list/detail responses
// carry display text. Both foreign keys are NOT NULL, LEFT JOIN + COALESCE
// only guards against hand-edited data.
const bookingDetailSelect = `SELECT b.id::text,b.booking_ref,b.experience_id::text,b.slot_id::text,b.traveler_id::text,b.traveler_name,b.traveler_email,b.traveler_phone,b.guests,b.total_paise,b.status,b.payment_status,b.created_at::text,
COALESCE(e.title,''),COALESCE(e.city,''),COALESCE(e.content,'{}'::jsonb),COALESCE(s.date::text,''),COALESCE(to_char(s.start_time,'HH24:MI'),'')
FROM experience_bookings b LEFT JOIN experiences e ON e.id=b.experience_id LEFT JOIN experience_slots s ON s.id=b.slot_id`

// scanBookingDetail scans bookingDetailSelect. On top of the scanBooking fields
// it adds title, experienceTitle, date, startTime, meetingPoint, city, photo
// and guestCount (meetingPoint comes from experiences.content, "" if absent).
func scanBookingDetail(row pgx.Row) (map[string]any, error) {
	var id, ref, exp, slot, traveler, name, email, phone, created string
	var title, city, date, start string
	var content []byte
	var guests int
	var total int64
	var status, payment string
	err := row.Scan(&id, &ref, &exp, &slot, &traveler, &name, &email, &phone, &guests, &total, &status, &payment, &created, &title, &city, &content, &date, &start)
	if err != nil {
		return nil, err
	}
	meta := map[string]any{}
	_ = json.Unmarshal(content, &meta)
	meeting := ""
	for _, k := range []string{"meetingPoint", "meeting_point", "meetingLocation"} {
		if v := portalcalc.Text(meta[k]); v != "" {
			meeting = v
			break
		}
	}
	return map[string]any{"_id": id, "bookingRef": ref, "experienceId": exp, "slotId": slot, "travelerId": traveler, "travelerName": name, "travelerEmail": email, "travelerPhone": phone, "guests": guests, "total": float64(total) / 100, "status": status, "paymentStatus": payment, "createdAt": created,
		"title": title, "experienceTitle": title, "date": date, "startTime": start, "meetingPoint": meeting, "city": city, "photo": portalcalc.PhotoFromMap(meta), "guestCount": guests}, nil
}

func scanBooking(row pgx.Row) (map[string]any, error) {
	var id, ref, exp, slot, traveler, name, email, phone, created string
	var guests int
	var total int64
	var status, payment string
	err := row.Scan(&id, &ref, &exp, &slot, &traveler, &name, &email, &phone, &guests, &total, &status, &payment, &created)
	return map[string]any{"_id": id, "bookingRef": ref, "experienceId": exp, "slotId": slot, "travelerId": traveler, "travelerName": name, "travelerEmail": email, "travelerPhone": phone, "guests": guests, "total": float64(total) / 100, "status": status, "paymentStatus": payment, "createdAt": created}, err
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
	}
	return 0
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
