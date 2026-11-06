package portalcalc

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits for the free-text fields of the ambassador messaging / visit
// endpoints and the optional timing fields of bulk and custom requests.
const (
	MaxMessageRunes    = 1000
	MaxVisitNotesRunes = 500
	MaxTimingNoteRunes = 200
)

var (
	uuidRe     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	hhmmRe     = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	strictDate = "2006-01-02"
)

// IsUUID reports whether s looks like a canonical UUID.
func IsUUID(s string) bool { return uuidRe.MatchString(s) }

// ValidateMessage trims a message to guests and checks it is 1..1000
// characters. It returns the trimmed text and an error message ("" when valid).
func ValidateMessage(s string) (string, string) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return "", "message is required"
	}
	if n > MaxMessageRunes {
		return "", "message must be at most 1000 characters"
	}
	return s, ""
}

// parseStrictDate parses exactly YYYY-MM-DD as an IST calendar date.
func parseStrictDate(s string) (time.Time, bool) {
	t, err := time.ParseInLocation(strictDate, s, IST)
	return t, err == nil
}

// ValidateVisitDate requires a strict YYYY-MM-DD that is today (IST) or later.
func ValidateVisitDate(s string, now time.Time) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "scheduledFor is required (YYYY-MM-DD)"
	}
	t, ok := parseStrictDate(s)
	if !ok {
		return "", "scheduledFor must be YYYY-MM-DD"
	}
	if t.Before(dateOnly(now)) {
		return "", "scheduledFor must not be in the past"
	}
	return s, ""
}

// ValidateVisitTime accepts an empty value or a 24-hour HH:mm.
func ValidateVisitTime(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if !hhmmRe.MatchString(s) {
		return "", "time must be HH:mm"
	}
	return s, ""
}

// ValidateVisitNotes trims notes and enforces the 500 character cap.
func ValidateVisitNotes(s string) (string, string) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > MaxVisitNotesRunes {
		return "", "notes must be at most 500 characters"
	}
	return s, ""
}

// ValidateVisitStatus checks the target status of a visit PATCH.
func ValidateVisitStatus(s string) (string, string) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "completed" || s == "cancelled" {
		return s, ""
	}
	return "", "status must be completed or cancelled"
}

// ValidateOptionalDate accepts an empty value or a strict YYYY-MM-DD.
func ValidateOptionalDate(s, field string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if _, ok := parseStrictDate(s); !ok {
		return "", field + " must be YYYY-MM-DD"
	}
	return s, ""
}

// ValidateTimingNote accepts an empty value or text of at most 200 characters.
func ValidateTimingNote(s string) (string, string) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > MaxTimingNoteRunes {
		return "", "timingNote must be at most 200 characters"
	}
	return s, ""
}

// NormalizeTiming validates the optional preferredDate and timingNote keys of
// a bulk-booking / custom-request payload in place (values are trimmed; null
// or empty values are removed). It returns an error message, "" when valid.
func NormalizeTiming(payload map[string]any) string {
	for _, f := range []struct {
		key      string
		validate func(string) (string, string)
	}{
		{"preferredDate", func(s string) (string, string) { return ValidateOptionalDate(s, "preferredDate") }},
		{"timingNote", ValidateTimingNote},
	} {
		v, present := payload[f.key]
		if !present {
			continue
		}
		if v == nil {
			delete(payload, f.key)
			continue
		}
		s, ok := v.(string)
		if !ok {
			return f.key + " must be a string"
		}
		clean, msg := f.validate(s)
		if msg != "" {
			return msg
		}
		if clean == "" {
			delete(payload, f.key)
		} else {
			payload[f.key] = clean
		}
	}
	return ""
}
