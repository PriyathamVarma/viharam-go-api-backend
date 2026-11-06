package portalcalc

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAssignmentNextStatus(t *testing.T) {
	cases := []struct {
		action, current, want string
		ok                    bool
	}{
		{"accept", "pending_acceptance", "accepted", true},
		{"accept", "", "accepted", true},
		{"accept", " Pending_Acceptance ", "accepted", true},
		{"accept", "accepted", "", false},
		{"accept", "rejected", "", false},
		{"accept", "completed", "", false},
		{"reject", "pending_acceptance", "rejected", true},
		{"reject", "accepted", "rejected", true},
		{"reject", "completed", "", false},
		{"reject", "rejected", "", false},
		{"proof", "accepted", "in_progress", true},
		{"proof", "in_progress", "in_progress", true},
		{"proof", "active", "active", true},
		{"proof", "pending_acceptance", "", false},
		{"proof", "completed", "", false},
		{"feedback", "accepted", "completed", true},
		{"feedback", "in_progress", "completed", true},
		{"feedback", "active", "completed", true},
		{"feedback", "pending_acceptance", "", false},
		{"feedback", "completed", "", false},
		{"unknown", "accepted", "", false},
	}
	for _, c := range cases {
		got, ok := AssignmentNextStatus(c.action, c.current)
		if got != c.want || ok != c.ok {
			t.Errorf("AssignmentNextStatus(%q,%q) = %q,%v want %q,%v", c.action, c.current, got, ok, c.want, c.ok)
		}
	}
}

func TestProofEntries(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want []string
	}{
		{"single url", map[string]any{"url": "https://x/a.jpg"}, []string{"https://x/a.jpg"}},
		{"urls list", map[string]any{"urls": []any{"https://x/a.jpg", " https://x/b.jpg "}}, []string{"https://x/a.jpg", "https://x/b.jpg"}},
		{"deduplicated", map[string]any{"url": "https://x/a.jpg", "urls": []any{"https://x/a.jpg", 5.0, ""}}, []string{"https://x/a.jpg"}},
		{"none", map[string]any{"caption": "no url"}, nil},
		{"wrong type", map[string]any{"url": 12.0}, nil},
	}
	for _, c := range cases {
		var got []string
		for _, e := range ProofEntries(c.body, "now") {
			got = append(got, e["url"].(string))
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	e := ProofEntries(map[string]any{"url": "u", "caption": "c"}, "now")[0]
	if e["type"] != "photo" || e["caption"] != "c" || e["uploadedAt"] != "now" {
		t.Errorf("entry defaults: %v", e)
	}
}

func TestAppendProofs(t *testing.T) {
	entry := []map[string]any{{"url": "new"}}
	cases := []struct {
		name string
		doc  map[string]any
		want int
	}{
		{"empty doc", map[string]any{}, 1},
		{"existing list", map[string]any{"proofs": []any{"a", "b"}}, 3},
		{"legacy object", map[string]any{"proof": map[string]any{"url": "old"}}, 2},
		{"list wins over legacy", map[string]any{"proofs": []any{"a"}, "proof": map[string]any{"url": "old"}}, 2},
		{"legacy wrong type", map[string]any{"proof": "x"}, 1},
	}
	for _, c := range cases {
		if got := AppendProofs(c.doc, entry); len(got) != c.want {
			t.Errorf("%s: len %d want %d", c.name, len(got), c.want)
		}
	}
}

func TestGuestFeedback(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want float64
	}{
		{"valid", map[string]any{"rating": 4.0, "notes": " great "}, 4},
		{"string rating", map[string]any{"rating": "5"}, 5},
		{"zero", map[string]any{"rating": 0.0}, 0},
		{"too high", map[string]any{"rating": 6.0}, 0},
		{"missing", map[string]any{"notes": "x"}, 0},
	}
	for _, c := range cases {
		got := GuestFeedback(c.body, "now")
		if c.want == 0 {
			if got != nil {
				t.Errorf("%s: expected nil, got %v", c.name, got)
			}
			continue
		}
		if got == nil || got["rating"] != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
	if GuestFeedback(map[string]any{"rating": 4.0, "notes": " great "}, "now")["notes"] != "great" {
		t.Error("notes should be trimmed")
	}
}

func TestOwnerProfilePatch(t *testing.T) {
	cases := []struct {
		name    string
		patch   map[string]any
		want    map[string]any
		wantErr bool
	}{
		{"all fields", map[string]any{"bio": " Hi ", "languages": []any{"Telugu", " ", "English"}, "specialties": []any{"Food"}, "isActive": false},
			map[string]any{"bio": "Hi", "languages": []string{"Telugu", "English"}, "specialties": []string{"Food"}, "isActive": false}, false},
		{"protected fields dropped", map[string]any{"bio": "x", "isVerified": true, "performanceScore": 99.0, "userId": "other", "avgGuestRating": 5.0},
			map[string]any{"bio": "x"}, false},
		{"only protected fields", map[string]any{"isVerified": true}, nil, true},
		{"empty", map[string]any{}, nil, true},
		{"bio wrong type", map[string]any{"bio": 5.0}, nil, true},
		{"bio too long", map[string]any{"bio": strings.Repeat("a", 2001)}, nil, true},
		{"languages wrong type", map[string]any{"languages": "Telugu"}, nil, true},
		{"language item wrong type", map[string]any{"languages": []any{1.0}}, nil, true},
		{"isActive wrong type", map[string]any{"isActive": "yes"}, nil, true},
	}
	for _, c := range cases {
		got, msg := OwnerProfilePatch(c.patch)
		if (msg != "") != c.wantErr {
			t.Errorf("%s: msg %q", c.name, msg)
			continue
		}
		if !c.wantErr && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestAvailabilityPatch(t *testing.T) {
	dates := []string{"2026-11-08", "2026-11-20"}
	cases := []struct {
		name    string
		body    map[string]any
		want    map[string]any
		wantErr bool
	}{
		{"unavailable dates mirrored", map[string]any{"unavailableDates": []any{"2026-11-20", "2026-11-08", "2026-11-08"}, "maxAssignmentsPerDay": 3.0},
			map[string]any{"unavailableDates": dates, "blockedDates": dates, "maxAssignmentsPerDay": 3}, false},
		{"blocked dates mirrored", map[string]any{"blockedDates": []any{"2026-11-08T00:00:00+05:30"}},
			map[string]any{"unavailableDates": []string{"2026-11-08"}, "blockedDates": []string{"2026-11-08"}}, false},
		{"clear dates", map[string]any{"unavailableDates": []any{}},
			map[string]any{"unavailableDates": []string{}, "blockedDates": []string{}}, false},
		{"max only", map[string]any{"maxAssignmentsPerDay": 1.0}, map[string]any{"maxAssignmentsPerDay": 1}, false},
		{"empty body", map[string]any{}, nil, true},
		{"bad date", map[string]any{"unavailableDates": []any{"tomorrow"}}, nil, true},
		{"dates not a list", map[string]any{"unavailableDates": "2026-11-08"}, nil, true},
		{"max zero", map[string]any{"maxAssignmentsPerDay": 0.0}, nil, true},
		{"max fractional", map[string]any{"maxAssignmentsPerDay": 1.5}, nil, true},
		{"max too large", map[string]any{"maxAssignmentsPerDay": 21.0}, nil, true},
		{"max string", map[string]any{"maxAssignmentsPerDay": "2"}, nil, true},
	}
	for _, c := range cases {
		got, msg := AvailabilityPatch(c.body)
		if (msg != "") != c.wantErr {
			t.Errorf("%s: msg %q", c.name, msg)
			continue
		}
		if !c.wantErr && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestMonthBounds(t *testing.T) {
	cases := []struct {
		in, start, end string
		ok             bool
	}{
		{"2026-11", "2026-11-01", "2026-12-01", true},
		{"2026-12", "2026-12-01", "2027-01-01", true},
		{" 2026-02 ", "2026-02-01", "2026-03-01", true},
		{"2026-13", "", "", false},
		{"2026-1", "", "", false},
		{"2026-11-01", "", "", false},
		{"", "", "", false},
		{"nov-2026", "", "", false},
	}
	for _, c := range cases {
		s, e, ok := MonthBounds(c.in)
		if s != c.start || e != c.end || ok != c.ok {
			t.Errorf("MonthBounds(%q) = %q,%q,%v want %q,%q,%v", c.in, s, e, ok, c.start, c.end, c.ok)
		}
	}
}

func TestCancelReason(t *testing.T) {
	if CancelReason("  changed plans ") != "changed plans" {
		t.Error("trim")
	}
	if CancelReason("") != "" {
		t.Error("empty")
	}
	long := strings.Repeat("é", 600)
	if got := CancelReason(long); len([]rune(got)) != 500 {
		t.Errorf("cap: %d runes", len([]rune(got)))
	}
}

func TestCompletedFromDoc(t *testing.T) {
	fallback := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		doc        map[string]any
		wantPaise  int64
		wantDate   string
		wantRating float64
	}{
		{"share and completedAt", map[string]any{"ambassadorShare": 300.0, "completedAt": "2026-11-02T10:00:00Z", "experienceDate": "2026-10-01"}, 30000, "2026-11-02", 0},
		{"share fallback and experienceDate", map[string]any{"share": 120.5, "experienceDate": "2026-10-20"}, 12050, "2026-10-20", 0},
		{"missing share", map[string]any{"completedAt": "2026-11-02"}, 0, "2026-11-02", 0},
		{"negative share", map[string]any{"ambassadorShare": -50.0}, 0, "2026-10-05", 0},
		{"string share", map[string]any{"ambassadorShare": "200"}, 20000, "2026-10-05", 0},
		{"unparsable dates use fallback", map[string]any{"completedAt": "soon", "experienceDate": 5.0}, 0, "2026-10-05", 0},
		{"rating", map[string]any{"guestFeedback": map[string]any{"rating": 4.0}}, 0, "2026-10-05", 4},
		{"rating out of range", map[string]any{"guestFeedback": map[string]any{"rating": 9.0}}, 0, "2026-10-05", 0},
		{"feedback wrong type", map[string]any{"guestFeedback": "great"}, 0, "2026-10-05", 0},
		{"empty doc", map[string]any{}, 0, "2026-10-05", 0},
	}
	for _, c := range cases {
		got := CompletedFromDoc(c.doc, fallback)
		if got.SharePaise != c.wantPaise || FormatDate(got.CompletedAt) != c.wantDate || got.Rating != c.wantRating {
			t.Errorf("%s: got %+v", c.name, got)
		}
	}
}
