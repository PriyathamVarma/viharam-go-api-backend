package portalcalc

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Assignment statuses used by the ambassador workflow.
const (
	StatusPendingAcceptance = "pending_acceptance"
	StatusAccepted          = "accepted"
	StatusRejected          = "rejected"
	StatusInProgress        = "in_progress"
	StatusCompleted         = "completed"

	// legacyStatusActive is the older spelling of in_progress.
	legacyStatusActive = "active"

	maxAssignmentsPerDayLimit = 20
	maxBioRunes               = 2000
	maxListItems              = 30
	maxDateItems              = 400
	maxListItemRunes          = 60
	maxCancelReasonRunes      = 500
)

// AssignmentNextStatus returns the status an assignment moves to when action
// ("accept", "reject", "proof" or "feedback") is applied to it, and false when
// the action is not allowed from the current status. A missing status counts
// as pending_acceptance.
func AssignmentNextStatus(action, current string) (string, bool) {
	cur := strings.ToLower(strings.TrimSpace(current))
	if cur == "" {
		cur = StatusPendingAcceptance
	}
	switch action {
	case "accept":
		if cur == StatusPendingAcceptance {
			return StatusAccepted, true
		}
	case "reject":
		if cur == StatusPendingAcceptance || cur == StatusAccepted {
			return StatusRejected, true
		}
	case "proof":
		switch cur {
		case StatusAccepted:
			return StatusInProgress, true
		case StatusInProgress, legacyStatusActive:
			return cur, true
		}
	case "feedback":
		switch cur {
		case StatusAccepted, StatusInProgress, legacyStatusActive:
			return StatusCompleted, true
		}
	}
	return "", false
}

// ProofEntries extracts the proof items of a proof upload body: the "url" and
// every element of "urls", de-duplicated, each with the shared type (default
// "photo") and caption. It returns nil when the body carries no URL.
func ProofEntries(body map[string]any, now string) []map[string]any {
	kind := Text(body["type"])
	if kind == "" {
		kind = "photo"
	}
	caption := Text(body["caption"])
	urls := []string{Text(body["url"])}
	if list, ok := body["urls"].([]any); ok {
		for _, v := range list {
			urls = append(urls, Text(v))
		}
	}
	seen := map[string]bool{}
	var out []map[string]any
	for _, u := range urls {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, map[string]any{"type": kind, "url": u, "caption": caption, "uploadedAt": now})
	}
	return out
}

// AppendProofs returns the assignment's existing proofs followed by entries.
// A legacy single "proof" object is kept as the first proof when there is no
// "proofs" list yet.
func AppendProofs(doc map[string]any, entries []map[string]any) []any {
	out := []any{}
	if list, ok := doc["proofs"].([]any); ok {
		out = append(out, list...)
	} else if legacy, ok := doc["proof"].(map[string]any); ok {
		out = append(out, legacy)
	}
	for _, e := range entries {
		out = append(out, e)
	}
	return out
}

// GuestFeedback builds the guestFeedback object read by the earnings
// endpoint from a completion report. It returns nil unless the body carries
// a rating between 1 and 5.
func GuestFeedback(body map[string]any, now string) map[string]any {
	rating := Number(body["rating"])
	if rating < 1 || rating > 5 {
		return nil
	}
	return map[string]any{"rating": rating, "notes": Text(body["notes"]), "submittedAt": now}
}

// OwnerProfilePatch validates the fields an ambassador may change on their
// own profile (bio, languages, specialties, isActive) and drops everything
// else, so verification and score fields cannot be edited. The second result
// is a client-facing error message, empty when the patch is acceptable.
func OwnerProfilePatch(patch map[string]any) (map[string]any, string) {
	out := map[string]any{}
	if v, ok := patch["bio"]; ok {
		s, isString := v.(string)
		if !isString || utf8.RuneCountInString(strings.TrimSpace(s)) > maxBioRunes {
			return nil, "bio must be text of at most 2000 characters"
		}
		out["bio"] = strings.TrimSpace(s)
	}
	for _, key := range []string{"languages", "specialties"} {
		v, ok := patch[key]
		if !ok {
			continue
		}
		list, msg := stringList(v, key, maxListItems)
		if msg != "" {
			return nil, msg
		}
		out[key] = list
	}
	if v, ok := patch["isActive"]; ok {
		b, isBool := v.(bool)
		if !isBool {
			return nil, "isActive must be true or false"
		}
		out["isActive"] = b
	}
	if len(out) == 0 {
		return nil, "No editable profile fields supplied"
	}
	return out, ""
}

// AvailabilityPatch validates an availability update and returns the fields
// to store at the top level of the profile. unavailableDates and blockedDates
// are merged, normalised to YYYY-MM-DD, de-duplicated and sorted, and both
// keys are returned so either reader sees the same list.
func AvailabilityPatch(body map[string]any) (map[string]any, string) {
	out := map[string]any{}
	seen := map[string]bool{}
	dates := []string{}
	given := false
	for _, key := range []string{"unavailableDates", "blockedDates"} {
		v, ok := body[key]
		if !ok {
			continue
		}
		given = true
		list, msg := stringList(v, key, maxDateItems)
		if msg != "" {
			return nil, msg
		}
		for _, s := range list {
			t, valid := ParseDate(s)
			if !valid {
				return nil, key + " must contain YYYY-MM-DD dates"
			}
			if d := FormatDate(t); !seen[d] {
				seen[d] = true
				dates = append(dates, d)
			}
		}
	}
	if given {
		sort.Strings(dates)
		out["unavailableDates"] = dates
		out["blockedDates"] = dates
	}
	if v, ok := body["maxAssignmentsPerDay"]; ok {
		n, isNumber := v.(float64)
		if !isNumber || n != math.Trunc(n) || n < 1 || n > maxAssignmentsPerDayLimit {
			return nil, "maxAssignmentsPerDay must be a whole number between 1 and 20"
		}
		out["maxAssignmentsPerDay"] = int(n)
	}
	if len(out) == 0 {
		return nil, "unavailableDates or maxAssignmentsPerDay is required"
	}
	return out, ""
}

// stringList reads a JSON array of at most limit short strings, trimming items
// and dropping empty ones.
func stringList(v any, key string, limit int) ([]string, string) {
	raw, ok := v.([]any)
	if !ok || len(raw) > limit {
		return nil, key + " must be a list of text values"
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		s, isString := e.(string)
		if !isString {
			return nil, key + " must be a list of text values"
		}
		if s = strings.TrimSpace(s); utf8.RuneCountInString(s) > maxListItemRunes {
			return nil, key + " contains a value that is too long"
		} else if s != "" {
			out = append(out, s)
		}
	}
	return out, ""
}

// MonthBounds turns "YYYY-MM" into the first day of that month and of the
// next one ("YYYY-MM-DD"), for a half-open date range.
func MonthBounds(month string) (start, end string, ok bool) {
	month = strings.TrimSpace(month)
	if len(month) != 7 {
		return "", "", false
	}
	t, err := time.Parse("2006-01", month)
	if err != nil {
		return "", "", false
	}
	return t.Format(dateLayout), t.AddDate(0, 1, 0).Format(dateLayout), true
}

// CancelReason trims a cancellation reason and caps its length.
func CancelReason(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxCancelReasonRunes {
		return s
	}
	return string([]rune(s)[:maxCancelReasonRunes])
}

// CompletedFromDoc converts an assignment document into a CompletedAssignment.
// The share is ambassadorShare (rupees), else share, and is 0 when missing or
// negative. Completion time is completedAt, else experienceDate, else
// fallback (the record's own timestamp). Ratings outside 1-5 are ignored.
func CompletedFromDoc(doc map[string]any, fallback time.Time) CompletedAssignment {
	share := Number(doc["ambassadorShare"])
	if share == 0 {
		share = Number(doc["share"])
	}
	if share < 0 || math.IsNaN(share) || math.IsInf(share, 0) {
		share = 0
	}
	at := fallback
	if t, ok := ParseInstant(Text(doc["completedAt"])); ok {
		at = t
	} else if t, ok := ParseInstant(Text(doc["experienceDate"])); ok {
		at = t
	}
	rating := 0.0
	if fb, ok := doc["guestFeedback"].(map[string]any); ok {
		if r := Number(fb["rating"]); r >= 1 && r <= 5 {
			rating = r
		}
	}
	return CompletedAssignment{SharePaise: Paise(share), CompletedAt: at, Rating: rating}
}
