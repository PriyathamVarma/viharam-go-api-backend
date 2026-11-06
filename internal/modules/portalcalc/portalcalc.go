// Package portalcalc holds the pure (database-free) calculations behind the
// host and ambassador portal endpoints: earnings roll-ups, payout schedules,
// listing status normalisation and small text helpers.
//
// There is no payouts ledger in the schema yet, so payouts are DERIVED from
// bookings/assignments with the fixed schedules documented on the constants
// below. Responses carry payoutsSource="derived" so clients can tell.
package portalcalc

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	// CommissionPercent is the platform commission taken from host earnings.
	CommissionPercent = 15
	// HostPayoutLagDays is how long after a stay ends before a host payout is
	// eligible; host payouts are then released on the next Monday.
	HostPayoutLagDays = 7
	// AmbassadorPayoutDay is the day of the month following completion on
	// which an ambassador's share is paid.
	AmbassadorPayoutDay = 21
	// PayoutsSource marks payout figures as derived rather than ledger-backed.
	PayoutsSource = "derived"
	// Currency is the only supported currency.
	Currency = "INR"

	dateLayout = "2006-01-02"
)

// IST is the business time zone used for month boundaries and "today".
var IST = time.FixedZone("IST", 5*3600+30*60)

// ParseDate parses a YYYY-MM-DD (or longer RFC3339 / timestamp-prefixed)
// string as a calendar date in IST.
func ParseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return dateOnly(t), true
	}
	if len(s) >= 10 {
		if t, err := time.ParseInLocation(dateLayout, s[:10], IST); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ParseInstant parses a timestamp or date; date-only values become IST midnight.
func ParseInstant(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	return ParseDate(s)
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.In(IST).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, IST)
}

// FormatDate renders a date as YYYY-MM-DD in IST.
func FormatDate(t time.Time) string { return dateOnly(t).Format(dateLayout) }

// Rupees converts paise to a rupee amount with two decimals.
func Rupees(paise int64) float64 { return math.Round(float64(paise)) / 100 }

// Paise converts a rupee amount to integer paise.
func Paise(rupees float64) int64 { return int64(math.Round(rupees * 100)) }

// Commission returns the platform commission (15%, half-up) on paise.
func Commission(paise int64) int64 { return (paise*CommissionPercent + 50) / 100 }

// RollToMonday returns the first Monday on or after t (IST date).
func RollToMonday(t time.Time) time.Time {
	d := dateOnly(t)
	return d.AddDate(0, 0, (1-int(d.Weekday())+7)%7)
}

// NextMondayAfter returns the first Monday strictly after t.
func NextMondayAfter(t time.Time) time.Time {
	return RollToMonday(dateOnly(t).AddDate(0, 0, 1))
}

// HostPayoutDate is when the host share for a stay ending on stayEnd is paid.
func HostPayoutDate(stayEnd time.Time) time.Time {
	return RollToMonday(dateOnly(stayEnd).AddDate(0, 0, HostPayoutLagDays))
}

// AmbassadorPayoutDate is the 21st of the month after completion.
func AmbassadorPayoutDate(completedAt time.Time) time.Time {
	y, m, _ := completedAt.In(IST).Date()
	return time.Date(y, m+1, AmbassadorPayoutDay, 0, 0, 0, 0, IST)
}

// NextAmbassadorPayoutAfter returns the next payout day strictly after t.
func NextAmbassadorPayoutAfter(t time.Time) time.Time {
	y, m, _ := t.In(IST).Date()
	d := time.Date(y, m, AmbassadorPayoutDay, 0, 0, 0, 0, IST)
	if !d.After(dateOnly(t)) {
		d = time.Date(y, m+1, AmbassadorPayoutDay, 0, 0, 0, 0, IST)
	}
	return d
}

func monthKey(t time.Time) string { return t.In(IST).Format("2006-01") }

func monthStarts(now time.Time, n int) []time.Time {
	y, m, _ := now.In(IST).Date()
	out := make([]time.Time, 0, n)
	for i := n - 1; i >= 0; i-- {
		out = append(out, time.Date(y, m-time.Month(i), 1, 0, 0, 0, 0, IST))
	}
	return out
}

func monthLabel(t time.Time) string { return t.Month().String()[:3] }

// ---- Host earnings ----

// HostBooking is one confirmed or completed booking on a host's listing.
type HostBooking struct {
	// Kind is "property" or "experience".
	Kind string
	// TotalPaise is the host-facing booking value (before commission).
	TotalPaise int64
	// StayEnd is the check-out date or experience date.
	StayEnd time.Time
	// Completed is true for status "completed"; false means "confirmed".
	Completed bool
}

// Money is a gross / commission / net split in rupees.
type Money struct {
	Gross              float64 `json:"gross"`
	PlatformCommission float64 `json:"platformCommission"`
	Net                float64 `json:"net"`
}

type acc struct{ gross, commission int64 }

func (a *acc) add(total int64) {
	a.gross += total
	a.commission += Commission(total)
}
func (a acc) money() Money {
	return Money{Gross: Rupees(a.gross), PlatformCommission: Rupees(a.commission), Net: Rupees(a.gross - a.commission)}
}

// HostMonth is one point of the last-6-months series.
type HostMonth struct {
	Month string `json:"month"`
	Label string `json:"label"`
	Money
}

// HostPayout is one (derived) payout.
type HostPayout struct {
	ID           string  `json:"id"`
	Date         string  `json:"date"`
	Amount       float64 `json:"amount"`
	BookingCount int     `json:"bookingCount"`
	Status       string  `json:"status"`
}

// HostEarnings is the GET /host/earnings payload.
type HostEarnings struct {
	Currency            string       `json:"currency"`
	CommissionRate      float64      `json:"commissionRate"`
	BookingCount        int          `json:"bookingCount"`
	Totals              Money        `json:"totals"`
	ThisMonth           Money        `json:"thisMonth"`
	LastMonth           Money        `json:"lastMonth"`
	PendingPayout       float64      `json:"pendingPayout"`
	PendingBookingCount int          `json:"pendingBookingCount"`
	NextPayoutDate      string       `json:"nextPayoutDate"`
	Monthly             []HostMonth  `json:"monthly"`
	PayoutHistory       []HostPayout `json:"payoutHistory"`
	BankLinked          bool         `json:"bankLinked"`
	PayoutsSource       string       `json:"payoutsSource"`
}

const maxPayoutHistory = 20

// HostSummarize rolls bookings up into the earnings payload.
func HostSummarize(bookings []HostBooking, now time.Time, bankLinked bool) HostEarnings {
	today := dateOnly(now)
	var total, thisM, lastM acc
	byMonth := map[string]*acc{}
	pending := acc{}
	pendingCount := 0
	var nextPending time.Time
	paid := map[string]*struct {
		net   int64
		count int
	}{}

	thisKey := monthKey(now)
	lastKey := monthKey(time.Date(today.Year(), today.Month()-1, 1, 0, 0, 0, 0, IST))

	for _, b := range bookings {
		total.add(b.TotalPaise)
		key := monthKey(b.StayEnd)
		if byMonth[key] == nil {
			byMonth[key] = &acc{}
		}
		byMonth[key].add(b.TotalPaise)
		switch key {
		case thisKey:
			thisM.add(b.TotalPaise)
		case lastKey:
			lastM.add(b.TotalPaise)
		}
		payDate := HostPayoutDate(b.StayEnd)
		net := b.TotalPaise - Commission(b.TotalPaise)
		if b.Completed && !payDate.After(today) {
			k := FormatDate(payDate)
			if paid[k] == nil {
				paid[k] = &struct {
					net   int64
					count int
				}{}
			}
			paid[k].net += net
			paid[k].count++
			continue
		}
		pending.gross += b.TotalPaise
		pending.commission += Commission(b.TotalPaise)
		pendingCount++
		if payDate.After(today) && (nextPending.IsZero() || payDate.Before(nextPending)) {
			nextPending = payDate
		}
	}

	next := nextPending
	if next.IsZero() {
		next = NextMondayAfter(today)
	}

	monthly := make([]HostMonth, 0, 6)
	for _, ms := range monthStarts(now, 6) {
		m := Money{}
		if a := byMonth[monthKey(ms)]; a != nil {
			m = a.money()
		}
		monthly = append(monthly, HostMonth{Month: monthKey(ms), Label: monthLabel(ms), Money: m})
	}

	dates := make([]string, 0, len(paid))
	for d := range paid {
		dates = append(dates, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	history := make([]HostPayout, 0, len(dates))
	for _, d := range dates {
		if len(history) == maxPayoutHistory {
			break
		}
		p := paid[d]
		history = append(history, HostPayout{ID: "payout-" + d, Date: d, Amount: Rupees(p.net), BookingCount: p.count, Status: "paid"})
	}

	return HostEarnings{
		Currency:            Currency,
		CommissionRate:      CommissionPercent / 100.0,
		BookingCount:        len(bookings),
		Totals:              total.money(),
		ThisMonth:           thisM.money(),
		LastMonth:           lastM.money(),
		PendingPayout:       Rupees(pending.gross - pending.commission),
		PendingBookingCount: pendingCount,
		NextPayoutDate:      FormatDate(next),
		Monthly:             monthly,
		PayoutHistory:       history,
		BankLinked:          bankLinked,
		PayoutsSource:       PayoutsSource,
	}
}

// ---- Ambassador earnings ----

// CompletedAssignment is one completed ambassador assignment.
type CompletedAssignment struct {
	SharePaise  int64
	CompletedAt time.Time
	// Rating is the guest rating (1-5); 0 means none recorded.
	Rating float64
}

// AmbassadorMonth is one point of the last-6-months series.
type AmbassadorMonth struct {
	Month  string  `json:"month"`
	Label  string  `json:"label"`
	Amount float64 `json:"amount"`
}

// AmbassadorEarnings is the GET /ambassador/earnings payload.
type AmbassadorEarnings struct {
	Currency         string            `json:"currency"`
	TotalEarned      float64           `json:"totalEarned"`
	ThisMonth        float64           `json:"thisMonth"`
	ExperiencesCount int               `json:"experiencesCount"`
	AvgRating        float64           `json:"avgRating"`
	Monthly          []AmbassadorMonth `json:"monthly"`
	PendingPayout    float64           `json:"pendingPayout"`
	NextPayoutDate   string            `json:"nextPayoutDate"`
	BankLinked       bool              `json:"bankLinked"`
	PayoutsSource    string            `json:"payoutsSource"`
}

// AmbassadorSummarize rolls completed assignments up into the earnings payload.
// fallbackRating is used when no assignment carries a guest rating.
func AmbassadorSummarize(done []CompletedAssignment, now time.Time, fallbackRating float64, bankLinked bool) AmbassadorEarnings {
	today := dateOnly(now)
	var total, thisM, pending int64
	var ratingSum float64
	ratingN := 0
	byMonth := map[string]int64{}
	var nextPending time.Time
	thisKey := monthKey(now)

	for _, a := range done {
		total += a.SharePaise
		key := monthKey(a.CompletedAt)
		byMonth[key] += a.SharePaise
		if key == thisKey {
			thisM += a.SharePaise
		}
		if a.Rating > 0 {
			ratingSum += a.Rating
			ratingN++
		}
		if pd := AmbassadorPayoutDate(a.CompletedAt); pd.After(today) {
			pending += a.SharePaise
			if nextPending.IsZero() || pd.Before(nextPending) {
				nextPending = pd
			}
		}
	}
	next := nextPending
	if next.IsZero() {
		next = NextAmbassadorPayoutAfter(today)
	}
	avg := fallbackRating
	if ratingN > 0 {
		avg = ratingSum / float64(ratingN)
	}
	monthly := make([]AmbassadorMonth, 0, 6)
	for _, ms := range monthStarts(now, 6) {
		monthly = append(monthly, AmbassadorMonth{Month: monthKey(ms), Label: monthLabel(ms), Amount: Rupees(byMonth[monthKey(ms)])})
	}
	return AmbassadorEarnings{
		Currency:         Currency,
		TotalEarned:      Rupees(total),
		ThisMonth:        Rupees(thisM),
		ExperiencesCount: len(done),
		AvgRating:        math.Round(avg*10) / 10,
		Monthly:          monthly,
		PendingPayout:    Rupees(pending),
		NextPayoutDate:   FormatDate(next),
		BankLinked:       bankLinked,
		PayoutsSource:    PayoutsSource,
	}
}

// ---- Listings ----

// PropertyStatus maps properties.status to the portal vocabulary.
func PropertyStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active":
		return "live"
	case "inactive":
		return "paused"
	case "draft", "archived":
		return strings.ToLower(strings.TrimSpace(s))
	}
	return s
}

// ExperienceStatus maps experiences.status to the portal vocabulary.
func ExperienceStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "published":
		return "live"
	case "pending_review":
		return "in_review"
	case "draft", "archived":
		return strings.ToLower(strings.TrimSpace(s))
	}
	return s
}

// CreatorStatus maps the free-form status of a creator-experience document.
// Documents created from the mobile app carry no status, which means the
// submission is waiting for review.
func CreatorStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "approved", "published", "active", "live":
		return "live"
	case "draft":
		return "draft"
	case "rejected":
		return "rejected"
	case "archived":
		return "archived"
	case "paused", "inactive":
		return "paused"
	}
	return "in_review"
}

// FirstPhoto finds the first usable image URL in a loosely-typed value: a
// string, a list of strings/objects, or an object with a url/src key.
func FirstPhoto(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []string:
		for _, s := range t {
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
	case []any:
		for _, e := range t {
			if s := FirstPhoto(e); s != "" {
				return s
			}
		}
	case map[string]any:
		for _, k := range []string{"url", "src", "uri"} {
			if s := FirstPhoto(t[k]); s != "" {
				return s
			}
		}
	}
	return ""
}

// PhotoFromMap looks for a cover image under the keys used by the apps.
func PhotoFromMap(m map[string]any) string {
	for _, k := range []string{"image", "photo", "coverImage", "heroImage", "thumbnail", "images", "photos", "gallery"} {
		if s := FirstPhoto(m[k]); s != "" {
			return s
		}
	}
	return ""
}

// Number reads a JSON-ish number (float64 or numeric string).
func Number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(n), "%g", &f); err == nil {
			return f
		}
	}
	return 0
}

// Text reads a trimmed string.
func Text(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }

// ---- Guests ----

var dietaryWords = []string{"vegetarian", "vegan", "jain", "halal", "kosher", "gluten", "lactose", "dairy", "allerg", "nut ", "nuts", "peanut", "diet", "eggless", "no onion", "no garlic", "pescatarian", "diabetic"}

// DietaryNotes returns the special-requests text when it mentions food or
// allergy needs, otherwise an empty string.
func DietaryNotes(specialRequests string) string {
	s := strings.TrimSpace(specialRequests)
	low := strings.ToLower(s) + " "
	for _, w := range dietaryWords {
		if strings.Contains(low, w) {
			return s
		}
	}
	return ""
}

// JoinNotes joins non-empty notes with "; ".
func JoinNotes(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}
