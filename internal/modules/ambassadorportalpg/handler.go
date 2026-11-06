// Package ambassadorportalpg serves the ambassador-facing read models used by
// the mobile app: derived earnings, the guests of an assignment and the host
// families in the ambassador's region.
//
// Ambassador assignments and profiles live in the JSONB document store
// (document_records, domains "ambassadorassignments" and "ambassadorprofiles").
// An assignment belongs to an ambassador through owner_id, which holds the
// ambassador's user id; payload.ambassadorId is accepted as well.
package ambassadorportalpg

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

// profile loads the signed-in ambassador's profile document, if any.
func (h *Handler) profile(c *gin.Context, userID string) map[string]any {
	var raw []byte
	err := h.db.Pool.QueryRow(c.Request.Context(), `SELECT payload FROM document_records
WHERE domain='ambassadorprofiles' AND (payload->>'userId'=$1 OR owner_id::text=$1) ORDER BY created_at LIMIT 1`, userID).Scan(&raw)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// bankLinked reports whether the profile has payout bank details on file.
func bankLinked(profile map[string]any) bool {
	if profile == nil {
		return false
	}
	for _, k := range []string{"bankLinked", "bankAccountLinked", "bankAccountConnected"} {
		if b, ok := profile[k].(bool); ok && b {
			return true
		}
	}
	if bank, ok := profile["bankDetails"].(map[string]any); ok {
		return portalcalc.Text(bank["accountNumber"]) != ""
	}
	return false
}

// Earnings handles GET /api/v1/ambassador/earnings.
func (h *Handler) Earnings(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	rows, err := h.db.Pool.Query(c.Request.Context(), `SELECT payload,updated_at FROM document_records
WHERE domain='ambassadorassignments' AND (owner_id::text=$1 OR payload->>'ambassadorId'=$1) AND payload->>'status'='completed'`, claims.UserID)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch earnings")
		return
	}
	done := make([]portalcalc.CompletedAssignment, 0)
	for rows.Next() {
		var raw []byte
		var updated time.Time
		if err := rows.Scan(&raw, &updated); err != nil {
			rows.Close()
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode earnings")
			return
		}
		var doc map[string]any
		if json.Unmarshal(raw, &doc) != nil {
			continue
		}
		done = append(done, portalcalc.CompletedFromDoc(doc, updated))
	}
	rows.Close()
	if rows.Err() != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch earnings")
		return
	}

	profile := h.profile(c, claims.UserID)
	fallback := 0.0
	if profile != nil {
		fallback = portalcalc.Number(profile["avgGuestRating"])
	}
	_ = httpx.OK(c, portalcalc.AmbassadorSummarize(done, time.Now(), fallback, bankLinked(profile)), "")
}

// Guest is one booking party attached to an assignment.
type Guest struct {
	Name            string `json:"name"`
	PartySize       int    `json:"partySize"`
	DietaryNotes    string `json:"dietaryNotes"`
	SpecialRequests string `json:"specialRequests"`
	Status          string `json:"status"`
}

// Guests handles GET /api/v1/ambassador-assignments/:id/guests.
func (h *Handler) Guests(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	ctx := c.Request.Context()
	var owner *string
	var raw []byte
	err := h.db.Pool.QueryRow(ctx, `SELECT owner_id::text,payload FROM document_records WHERE id=$1::uuid AND domain='ambassadorassignments'`, c.Param("id")).Scan(&owner, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Assignment not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		doc = map[string]any{}
	}
	admin := claims.Role == "Admin" || claims.Role == "SuperAdmin"
	assigned := (owner != nil && *owner == claims.UserID) || portalcalc.Text(doc["ambassadorId"]) == claims.UserID
	if !admin && !assigned {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	switch portalcalc.Text(doc["status"]) {
	case "rejected", "cancelled":
		_ = httpx.Fail(c, 403, "forbidden", "This assignment is no longer active")
		return
	}

	bookingID := portalcalc.Text(doc["bookingId"])
	guests := make([]Guest, 0, 1)
	ref := ""
	party := 0
	if bookingID != "" {
		var name, special, status, allergies string
		var count int
		err := h.db.Pool.QueryRow(ctx, `SELECT b.booking_ref,b.traveler_name,b.guests,COALESCE(b.special_requests,''),b.status,
COALESCE((SELECT s.payload->>'allergies' FROM document_records s WHERE s.domain='travellersafeties' AND s.owner_id=b.traveler_id ORDER BY s.created_at DESC LIMIT 1),'')
FROM experience_bookings b WHERE b.id::text=$1`, bookingID).Scan(&ref, &name, &count, &special, &status, &allergies)
		switch {
		case err == nil:
			notes := portalcalc.JoinNotes(portalcalc.DietaryNotes(special), prefix("Allergies: ", allergies))
			guests = append(guests, Guest{Name: name, PartySize: count, DietaryNotes: notes, SpecialRequests: special, Status: status})
			party = count
		case errors.Is(err, pgx.ErrNoRows):
			// Booking removed or id not in this store: return an empty guest list.
		default:
			_ = httpx.Fail(c, 500, "database_error", "Failed to fetch guests")
			return
		}
	}
	_ = httpx.OK(c, gin.H{
		"assignmentId": c.Param("id"),
		"bookingId":    bookingID,
		"bookingRef":   ref,
		"partySize":    party,
		"guests":       guests,
	}, "")
}

// HostFamily is one host in the ambassador's region.
type HostFamily struct {
	ID                 string  `json:"id"`
	LegacyID           string  `json:"_id"`
	UserID             string  `json:"userId"`
	Name               string  `json:"name"`
	HostType           string  `json:"hostType"`
	Village            string  `json:"village"`
	District           string  `json:"district"`
	State              string  `json:"state"`
	ListingsCount      int64   `json:"listingsCount"`
	VerificationStatus string  `json:"verificationStatus"`
	LastVisited        *string `json:"lastVisited"`
}

// Hosts handles GET /api/v1/ambassador/hosts. A host is in the ambassador's
// region when its address village/district matches one of the profile's
// cities, when the profile has no cities and the host's state matches one of
// its states, when one of its experiences is in one of the cities, or when the
// ambassador has been assigned to one of its experiences.
func (h *Handler) Hosts(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	profile := h.profile(c, claims.UserID)
	cities := lowerList(profile, "cities", "city")
	states := lowerList(profile, "states", "state")

	rows, err := h.db.Pool.Query(c.Request.Context(), `
WITH visits AS (
  SELECT COALESCE(NULLIF(a.payload->>'experienceId',''),b.experience_id::text) AS exp_id,
         NULLIF(a.payload->>'experienceDate','') AS day,
         a.payload->>'status' AS status
  FROM document_records a
  LEFT JOIN experience_bookings b ON b.id::text=a.payload->>'bookingId'
  WHERE a.domain='ambassadorassignments' AND (a.owner_id::text=$1 OR a.payload->>'ambassadorId'=$1)
)
SELECT hp.id::text,hp.user_id::text,hp.display_name,hp.host_type,
  COALESCE(hp.address->>'village',''),COALESCE(hp.address->>'district',''),COALESCE(hp.address->>'state',''),
  (SELECT count(*) FROM experiences e WHERE e.host_id=hp.user_id AND e.status<>'archived')
   + (SELECT count(*) FROM properties p WHERE p.host_id=hp.user_id AND p.status<>'archived'),
  hp.verification_status,
  (SELECT max(v.day) FROM visits v WHERE v.status='completed' AND v.exp_id IN (SELECT e.id::text FROM experiences e WHERE e.host_id=hp.user_id)),
  (SELECT max(av.scheduled_for)::text FROM ambassador_visits av WHERE av.host_profile_id=hp.id AND av.ambassador_user_id::text=$1 AND av.status='completed')
FROM host_profiles hp
WHERE lower(hp.address->>'village')=ANY($2::text[])
   OR lower(hp.address->>'district')=ANY($2::text[])
   OR (cardinality($2::text[])=0 AND lower(hp.address->>'state')=ANY($3::text[]))
   OR EXISTS (SELECT 1 FROM experiences e WHERE e.host_id=hp.user_id AND lower(e.city)=ANY($2::text[]))
   OR EXISTS (SELECT 1 FROM experiences e WHERE e.host_id=hp.user_id AND e.id::text IN (SELECT exp_id FROM visits))
ORDER BY hp.display_name
LIMIT 100`, claims.UserID, cities, states)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch hosts")
		return
	}
	hosts := make([]HostFamily, 0)
	for rows.Next() {
		var f HostFamily
		var last, lastVisit *string
		if err := rows.Scan(&f.ID, &f.UserID, &f.Name, &f.HostType, &f.Village, &f.District, &f.State, &f.ListingsCount, &f.VerificationStatus, &last, &lastVisit); err != nil {
			rows.Close()
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode hosts")
			return
		}
		f.LegacyID = f.ID
		// lastVisited is the latest of completed assignments and completed visits.
		var best time.Time
		for _, d := range []*string{last, lastVisit} {
			if d == nil {
				continue
			}
			if t, ok := portalcalc.ParseDate(*d); ok && t.After(best) {
				best = t
			}
		}
		if !best.IsZero() {
			s := portalcalc.FormatDate(best)
			f.LastVisited = &s
		}
		hosts = append(hosts, f)
	}
	rows.Close()
	if rows.Err() != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch hosts")
		return
	}
	_ = httpx.OK(c, gin.H{"hosts": hosts, "total": len(hosts)}, "")
}

// lowerList merges a string-array key and a single-string key of the profile
// into a lower-cased, non-nil list (a nil slice would be sent as SQL NULL).
func lowerList(profile map[string]any, listKey, singleKey string) []string {
	out := []string{}
	add := func(s string) {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	if profile == nil {
		return out
	}
	if arr, ok := profile[listKey].([]any); ok {
		for _, v := range arr {
			add(portalcalc.Text(v))
		}
	}
	add(portalcalc.Text(profile[singleKey]))
	return out
}

func prefix(p, s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return p + strings.TrimSpace(s)
}
