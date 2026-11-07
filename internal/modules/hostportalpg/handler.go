// Package hostportalpg serves the host-facing read models used by the mobile
// app: the host's listings and a derived earnings summary.
package hostportalpg

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/modules/portalcalc"
	"github.com/viharam/go-api-backend/internal/platform/database"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

type Handler struct{ db *database.Postgres }

func NewHandler(db *database.Postgres) *Handler { return &Handler{db: db} }

// Listing is one property or experience owned by the host.
type Listing struct {
	ID           string  `json:"id"`
	LegacyID     string  `json:"_id"`
	Kind         string  `json:"kind"` // property | experience | creator_experience
	Title        string  `json:"title"`
	Subtitle     string  `json:"subtitle"`
	Status       string  `json:"status"` // live | in_review | draft | paused | rejected | archived
	RawStatus    string  `json:"rawStatus"`
	Price        float64 `json:"price"` // rupees
	PriceUnit    string  `json:"priceUnit"`
	City         string  `json:"city"`
	Photo        string  `json:"photo"`
	BookingCount int64   `json:"bookingCount"`
	CreatedAt    string  `json:"createdAt,omitempty"`
}

func newListing(id, kind string) Listing { return Listing{ID: id, LegacyID: id, Kind: kind} }

// Listings handles GET /api/v1/host/listings.
func (h *Handler) Listings(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	ctx := c.Request.Context()
	uid := claims.UserID
	listings := make([]Listing, 0)

	// Properties.
	rows, err := h.db.Pool.Query(ctx, `SELECT p.id::text,p.name,p.type,p.status,p.city,p.price_per_night_paise,COALESCE(p.image,''),p.images,
(SELECT count(*) FROM property_bookings b WHERE b.property_id=p.id AND b.status IN ('confirmed','completed')),p.created_at::text
FROM properties p WHERE p.host_id=$1::uuid ORDER BY p.updated_at DESC`, uid)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch listings")
		return
	}
	for rows.Next() {
		var id, name, typ, status, city, image, created string
		var price, count int64
		var images []byte
		if err := rows.Scan(&id, &name, &typ, &status, &city, &price, &image, &images, &count, &created); err != nil {
			rows.Close()
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode listings")
			return
		}
		photo := image
		if photo == "" {
			var decoded any
			if json.Unmarshal(images, &decoded) == nil {
				photo = portalcalc.FirstPhoto(decoded)
			}
		}
		l := newListing(id, "property")
		l.Title, l.Subtitle, l.Status, l.RawStatus = name, typ, portalcalc.PropertyStatus(status), status
		l.Price, l.PriceUnit, l.City, l.Photo, l.BookingCount, l.CreatedAt = portalcalc.Rupees(price), "night", city, photo, count, created
		listings = append(listings, l)
	}
	rows.Close()
	if rows.Err() != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch listings")
		return
	}

	// Experiences in the relational catalogue.
	rows, err = h.db.Pool.Query(ctx, `SELECT e.id::text,e.title,e.category,e.city,e.status,e.price_per_person_paise,e.duration_minutes,e.content,
(SELECT count(*) FROM experience_bookings b WHERE b.experience_id=e.id AND b.status IN ('confirmed','completed')),e.created_at::text
FROM experiences e WHERE e.host_id=$1::uuid ORDER BY e.updated_at DESC`, uid)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch listings")
		return
	}
	for rows.Next() {
		var id, title, category, city, status, created string
		var price, count int64
		var minutes int
		var content []byte
		if err := rows.Scan(&id, &title, &category, &city, &status, &price, &minutes, &content, &count, &created); err != nil {
			rows.Close()
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode listings")
			return
		}
		var meta map[string]any
		_ = json.Unmarshal(content, &meta)
		l := newListing(id, "experience")
		l.Title, l.Subtitle = title, category
		if d := durationLabel(minutes); d != "" {
			l.Subtitle += " · " + d
		}
		l.Status, l.RawStatus = portalcalc.ExperienceStatus(status), status
		l.Price, l.PriceUnit, l.City, l.Photo, l.BookingCount, l.CreatedAt = portalcalc.Rupees(price), "person", city, portalcalc.PhotoFromMap(meta), count, created
		listings = append(listings, l)
	}
	rows.Close()
	if rows.Err() != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch listings")
		return
	}

	// Experiences submitted from the apps (POST /creator-experiences) live in
	// the JSONB document store until they are reviewed and promoted.
	rows, err = h.db.Pool.Query(ctx, `SELECT id::text,payload,created_at::text FROM document_records WHERE domain='creatorexperiences' AND owner_id=$1::uuid AND NOT EXISTS (SELECT 1 FROM experiences e WHERE e.id=document_records.id) ORDER BY created_at DESC`, uid)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch listings")
		return
	}
	for rows.Next() {
		var id, created string
		var payload []byte
		if err := rows.Scan(&id, &payload, &created); err != nil {
			rows.Close()
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode listings")
			return
		}
		var doc map[string]any
		if json.Unmarshal(payload, &doc) != nil {
			continue
		}
		status := portalcalc.Text(doc["status"])
		l := newListing(id, "creator_experience")
		l.Title = portalcalc.Text(doc["title"])
		l.Subtitle = portalcalc.Text(doc["category"])
		if d := portalcalc.Text(doc["duration"]); d != "" {
			l.Subtitle += " · " + d
		}
		l.Status, l.RawStatus = portalcalc.CreatorStatus(status), status
		l.Price, l.PriceUnit = portalcalc.Number(doc["pricePerPerson"]), "person"
		l.City = firstNonEmpty(portalcalc.Text(doc["city"]), portalcalc.Text(doc["location"]))
		l.Photo, l.CreatedAt = portalcalc.PhotoFromMap(doc), created
		listings = append(listings, l)
	}
	rows.Close()
	if rows.Err() != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch listings")
		return
	}

	sort.SliceStable(listings, func(i, j int) bool { return kindOrder(listings[i].Kind) < kindOrder(listings[j].Kind) })
	_ = httpx.OK(c, gin.H{"listings": listings, "total": len(listings)}, "")
}

// Earnings handles GET /api/v1/host/earnings.
func (h *Handler) Earnings(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	ctx := c.Request.Context()
	uid := claims.UserID
	bookings := make([]portalcalc.HostBooking, 0)

	// Property stays: value is the booking total; the stay ends at check-out.
	rows, err := h.db.Pool.Query(ctx, `SELECT COALESCE(b.total_price_paise,0),COALESCE(b.check_out::text,b.created_at::text,''),b.status FROM property_bookings b WHERE b.host_id=$1::uuid AND b.status IN ('confirmed','completed')`, uid)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch earnings")
		return
	}
	for rows.Next() {
		var total int64
		var day, status string
		if err := rows.Scan(&total, &day, &status); err != nil {
			rows.Close()
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode earnings")
			return
		}
		if end, ok := portalcalc.ParseDate(day); ok {
			bookings = append(bookings, portalcalc.HostBooking{Kind: "property", TotalPaise: total, StayEnd: end, Completed: status == "completed"})
		}
	}
	rows.Close()
	if rows.Err() != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch earnings")
		return
	}

	// Experiences: the host earns the subtotal; the traveller service fee is the platform's.
	rows, err = h.db.Pool.Query(ctx, `SELECT COALESCE(b.subtotal_paise,0),COALESCE(s.date::text,b.completed_at::text,b.created_at::text,''),b.status FROM experience_bookings b
JOIN experiences e ON e.id=b.experience_id JOIN experience_slots s ON s.id=b.slot_id
WHERE e.host_id=$1::uuid AND b.status IN ('confirmed','completed')`, uid)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch earnings")
		return
	}
	for rows.Next() {
		var total int64
		var day, status string
		if err := rows.Scan(&total, &day, &status); err != nil {
			rows.Close()
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode earnings")
			return
		}
		if end, ok := portalcalc.ParseDate(day); ok {
			bookings = append(bookings, portalcalc.HostBooking{Kind: "experience", TotalPaise: total, StayEnd: end, Completed: status == "completed"})
		}
	}
	rows.Close()
	if rows.Err() != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch earnings")
		return
	}

	bankLinked := false
	var bank []byte
	if err := h.db.Pool.QueryRow(ctx, `SELECT bank_details FROM host_profiles WHERE user_id=$1::uuid`, uid).Scan(&bank); err == nil {
		var m map[string]any
		if json.Unmarshal(bank, &m) == nil {
			bankLinked = portalcalc.Text(m["accountNumber"]) != ""
		}
	}

	_ = httpx.OK(c, portalcalc.HostSummarize(bookings, time.Now(), bankLinked), "")
}

func kindOrder(kind string) int {
	switch kind {
	case "property":
		return 0
	case "experience":
		return 1
	}
	return 2
}

func durationLabel(minutes int) string {
	switch {
	case minutes <= 0:
		return ""
	case minutes%60 == 0 && minutes/60 == 1:
		return "1 hour"
	case minutes%60 == 0:
		return fmt.Sprintf("%d hours", minutes/60)
	case minutes < 60:
		return fmt.Sprintf("%d min", minutes)
	}
	return fmt.Sprintf("%.1f hours", float64(minutes)/60)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
