package portalcalc

import (
	"strings"
	"testing"
	"time"
)

func TestValidateMessage(t *testing.T) {
	cases := []struct {
		name, in, want string
		bad            bool
	}{
		{"empty", "", "", true},
		{"spaces", "   \n", "", true},
		{"ok", "  Hello  ", "Hello", false},
		{"max", strings.Repeat("a", 1000), strings.Repeat("a", 1000), false},
		{"too long", strings.Repeat("a", 1001), "", true},
		{"multibyte counted as runes", strings.Repeat("అ", 1000), strings.Repeat("అ", 1000), false},
		{"multibyte too long", strings.Repeat("అ", 1001), "", true},
	}
	for _, c := range cases {
		got, msg := ValidateMessage(c.in)
		if (msg != "") != c.bad || got != c.want {
			t.Errorf("%s: got (%d runes, %q)", c.name, len([]rune(got)), msg)
		}
	}
}

func TestValidateVisitDate(t *testing.T) {
	now := time.Date(2026, 11, 6, 20, 0, 0, 0, time.UTC) // 2026-11-07 01:30 IST
	cases := []struct {
		in  string
		bad bool
	}{
		{"2026-11-07", false},
		{"2026-11-06", true}, // yesterday in IST
		{"2026-12-01", false},
		{"2026-1-5", true},
		{"06/11/2026", true},
		{"2026-02-30", true},
		{"", true},
		{"2026-11-07T10:00:00Z", true},
	}
	for _, c := range cases {
		_, msg := ValidateVisitDate(c.in, now)
		if (msg != "") != c.bad {
			t.Errorf("%q: msg=%q", c.in, msg)
		}
	}
}

func TestValidateVisitTime(t *testing.T) {
	cases := []struct {
		in  string
		bad bool
	}{
		{"", false}, {"09:30", false}, {"00:00", false}, {"23:59", false},
		{"24:00", true}, {"9:30", true}, {"09:60", true}, {"09:30:00", true}, {"ab:cd", true},
	}
	for _, c := range cases {
		_, msg := ValidateVisitTime(c.in)
		if (msg != "") != c.bad {
			t.Errorf("%q: msg=%q", c.in, msg)
		}
	}
}

func TestValidateVisitNotesAndStatus(t *testing.T) {
	if _, m := ValidateVisitNotes(strings.Repeat("x", 500)); m != "" {
		t.Error("500 should pass")
	}
	if _, m := ValidateVisitNotes(strings.Repeat("x", 501)); m == "" {
		t.Error("501 should fail")
	}
	for in, bad := range map[string]bool{"completed": false, " Cancelled ": false, "scheduled": true, "": true} {
		if _, m := ValidateVisitStatus(in); (m != "") != bad {
			t.Errorf("status %q: %q", in, m)
		}
	}
}

func TestNormalizeTiming(t *testing.T) {
	cases := []struct {
		name    string
		in      map[string]any
		bad     bool
		wantKey map[string]any
	}{
		{"absent", map[string]any{}, false, map[string]any{}},
		{"ok", map[string]any{"preferredDate": " 2027-01-10 ", "timingNote": " January, 2 days "}, false, map[string]any{"preferredDate": "2027-01-10", "timingNote": "January, 2 days"}},
		{"empty dropped", map[string]any{"preferredDate": "", "timingNote": nil}, false, map[string]any{}},
		{"bad date", map[string]any{"preferredDate": "10-01-2027"}, true, nil},
		{"date not string", map[string]any{"preferredDate": 5}, true, nil},
		{"long note", map[string]any{"timingNote": strings.Repeat("n", 201)}, true, nil},
		{"note 200", map[string]any{"timingNote": strings.Repeat("n", 200)}, false, map[string]any{"timingNote": strings.Repeat("n", 200)}},
	}
	for _, c := range cases {
		msg := NormalizeTiming(c.in)
		if (msg != "") != c.bad {
			t.Errorf("%s: msg=%q", c.name, msg)
			continue
		}
		if !c.bad {
			if len(c.in) != len(c.wantKey) {
				t.Errorf("%s: got %v", c.name, c.in)
			}
			for k, v := range c.wantKey {
				if c.in[k] != v {
					t.Errorf("%s: %s=%v", c.name, k, c.in[k])
				}
			}
		}
	}
}

func TestIsUUID(t *testing.T) {
	if !IsUUID("3f2b1c4e-1111-4222-8333-444455556666") || IsUUID("nope") || IsUUID("") {
		t.Error("uuid check")
	}
}
