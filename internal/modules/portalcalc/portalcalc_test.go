package portalcalc

import (
	"testing"
	"time"
)

func d(s string) time.Time {
	t, ok := ParseDate(s)
	if !ok {
		panic("bad date " + s)
	}
	return t
}

func TestCommissionIsFifteenPercentHalfUp(t *testing.T) {
	cases := map[int64]int64{0: 0, 100: 15, 1000: 150, 10: 2, 3: 0, 4: 1, 250000: 37500}
	for in, want := range cases {
		if got := Commission(in); got != want {
			t.Errorf("Commission(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestRollToMonday(t *testing.T) {
	// 2026-11-02 is a Monday.
	cases := map[string]string{
		"2026-11-02": "2026-11-02", // Monday stays
		"2026-11-03": "2026-11-09", // Tuesday
		"2026-11-08": "2026-11-09", // Sunday
		"2026-11-07": "2026-11-09", // Saturday
	}
	for in, want := range cases {
		if got := FormatDate(RollToMonday(d(in))); got != want {
			t.Errorf("RollToMonday(%s) = %s, want %s", in, got, want)
		}
	}
	if got := FormatDate(NextMondayAfter(d("2026-11-02"))); got != "2026-11-09" {
		t.Errorf("NextMondayAfter(Monday) = %s", got)
	}
}

func TestAmbassadorPayoutDates(t *testing.T) {
	if got := FormatDate(AmbassadorPayoutDate(d("2026-10-30"))); got != "2026-11-21" {
		t.Errorf("got %s", got)
	}
	if got := FormatDate(AmbassadorPayoutDate(d("2026-12-05"))); got != "2027-01-21" {
		t.Errorf("year rollover: got %s", got)
	}
	if got := FormatDate(NextAmbassadorPayoutAfter(d("2026-11-06"))); got != "2026-11-21" {
		t.Errorf("got %s", got)
	}
	if got := FormatDate(NextAmbassadorPayoutAfter(d("2026-11-21"))); got != "2026-12-21" {
		t.Errorf("on the 21st the next payout is next month, got %s", got)
	}
}

func TestHostSummarize(t *testing.T) {
	now := d("2026-11-06").Add(10 * time.Hour) // Friday
	bookings := []HostBooking{
		// Completed, stay ended 1 Oct: payout 12 Oct (Monday) -> paid.
		{Kind: "property", TotalPaise: 1000000, StayEnd: d("2026-10-01"), Completed: true},
		// Completed, stay ended 1 Oct, same payout date.
		{Kind: "experience", TotalPaise: 200000, StayEnd: d("2026-10-01"), Completed: true},
		// Completed 4 Nov: payout 16 Nov (Monday, 7 days + roll) -> pending.
		{Kind: "property", TotalPaise: 500000, StayEnd: d("2026-11-04"), Completed: true},
		// Confirmed, future stay 20 Nov: payout 30 Nov (Monday) -> pending.
		{Kind: "experience", TotalPaise: 300000, StayEnd: d("2026-11-20")},
		// Confirmed but stay long past: never completed, stays pending.
		{Kind: "property", TotalPaise: 100000, StayEnd: d("2026-06-01")},
	}
	got := HostSummarize(bookings, now, true)

	if got.BookingCount != 5 {
		t.Errorf("BookingCount = %d", got.BookingCount)
	}
	if got.Totals.Gross != 21000 || got.Totals.PlatformCommission != 3150 || got.Totals.Net != 17850 {
		t.Errorf("Totals = %+v", got.Totals)
	}
	if got.Totals.Gross-got.Totals.PlatformCommission != got.Totals.Net {
		t.Error("net must equal gross minus commission")
	}
	if got.ThisMonth.Gross != 8000 { // 4 Nov + 20 Nov
		t.Errorf("ThisMonth = %+v", got.ThisMonth)
	}
	if got.LastMonth.Gross != 12000 {
		t.Errorf("LastMonth = %+v", got.LastMonth)
	}
	// Pending: 5000 + 3000 + 1000 = 9000 gross, 85% net = 7650.
	if got.PendingPayout != 7650 || got.PendingBookingCount != 3 {
		t.Errorf("Pending = %v (%d)", got.PendingPayout, got.PendingBookingCount)
	}
	if got.NextPayoutDate != "2026-11-16" {
		t.Errorf("NextPayoutDate = %s", got.NextPayoutDate)
	}
	if len(got.PayoutHistory) != 1 || got.PayoutHistory[0].Date != "2026-10-08" && got.PayoutHistory[0].Date != "2026-10-12" {
		t.Fatalf("PayoutHistory = %+v", got.PayoutHistory)
	}
	// 1 Oct + 7 days = 8 Oct (Thursday) -> Monday 12 Oct.
	if got.PayoutHistory[0].Date != "2026-10-12" || got.PayoutHistory[0].Amount != 10200 || got.PayoutHistory[0].BookingCount != 2 {
		t.Errorf("PayoutHistory[0] = %+v", got.PayoutHistory[0])
	}
	if len(got.Monthly) != 6 || got.Monthly[5].Month != "2026-11" || got.Monthly[0].Month != "2026-06" {
		t.Errorf("Monthly = %+v", got.Monthly)
	}
	if got.Monthly[0].Gross != 1000 || got.Monthly[4].Gross != 12000 || got.Monthly[5].Net != 6800 {
		t.Errorf("Monthly values = %+v", got.Monthly)
	}
	if !got.BankLinked || got.PayoutsSource != "derived" || got.CommissionRate != 0.15 {
		t.Errorf("flags = %+v", got)
	}
}

func TestHostSummarizeEmpty(t *testing.T) {
	now := d("2026-11-06")
	got := HostSummarize(nil, now, false)
	if got.Totals.Gross != 0 || got.PendingPayout != 0 || len(got.Monthly) != 6 || got.PayoutHistory == nil {
		t.Errorf("empty = %+v", got)
	}
	if got.NextPayoutDate != "2026-11-09" {
		t.Errorf("next Monday after Fri 6 Nov = %s", got.NextPayoutDate)
	}
}

func TestAmbassadorSummarize(t *testing.T) {
	now := d("2026-11-06")
	done := []CompletedAssignment{
		{SharePaise: 48000, CompletedAt: d("2026-11-02"), Rating: 5}, // pays 21 Dec
		{SharePaise: 72000, CompletedAt: d("2026-10-20"), Rating: 4}, // pays 21 Nov
		{SharePaise: 40000, CompletedAt: d("2026-09-15"), Rating: 0}, // paid 21 Oct
		{SharePaise: 0, CompletedAt: d("2026-11-03"), Rating: 4.5},   // no share set
	}
	got := AmbassadorSummarize(done, now, 4.8, true)
	if got.TotalEarned != 1600 || got.ThisMonth != 480 || got.ExperiencesCount != 4 {
		t.Errorf("totals = %+v", got)
	}
	if got.PendingPayout != 1200 || got.NextPayoutDate != "2026-11-21" {
		t.Errorf("pending = %v next = %s", got.PendingPayout, got.NextPayoutDate)
	}
	if got.AvgRating != 4.5 { // (5+4+4.5)/3 = 4.5
		t.Errorf("AvgRating = %v", got.AvgRating)
	}
	if len(got.Monthly) != 6 || got.Monthly[5].Amount != 480 || got.Monthly[4].Amount != 720 || got.Monthly[3].Amount != 400 {
		t.Errorf("Monthly = %+v", got.Monthly)
	}
	if !got.BankLinked {
		t.Error("bank flag lost")
	}
}

func TestAmbassadorSummarizeEmptyUsesFallbackRating(t *testing.T) {
	got := AmbassadorSummarize(nil, d("2026-11-21"), 4.8, false)
	if got.AvgRating != 4.8 || got.TotalEarned != 0 || got.Monthly == nil {
		t.Errorf("empty = %+v", got)
	}
	if got.NextPayoutDate != "2026-12-21" {
		t.Errorf("NextPayoutDate = %s", got.NextPayoutDate)
	}
}

func TestStatusMapping(t *testing.T) {
	if PropertyStatus("active") != "live" || PropertyStatus("inactive") != "paused" || PropertyStatus("draft") != "draft" {
		t.Error("property status")
	}
	if ExperienceStatus("published") != "live" || ExperienceStatus("pending_review") != "in_review" {
		t.Error("experience status")
	}
	for in, want := range map[string]string{"": "in_review", "review": "in_review", "approved": "live", "Draft": "draft", "rejected": "rejected"} {
		if got := CreatorStatus(in); got != want {
			t.Errorf("CreatorStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstPhoto(t *testing.T) {
	m := map[string]any{"images": []any{"", map[string]any{"url": " https://x/y.jpg "}}}
	if got := PhotoFromMap(m); got != "https://x/y.jpg" {
		t.Errorf("got %q", got)
	}
	if PhotoFromMap(map[string]any{"image": "a.jpg", "images": []any{"b.jpg"}}) != "a.jpg" {
		t.Error("image should win")
	}
	if PhotoFromMap(nil) != "" || FirstPhoto(42) != "" {
		t.Error("expected empty")
	}
}

func TestDietaryNotes(t *testing.T) {
	if DietaryNotes("We are vegetarian, one allergic to peanuts") == "" {
		t.Error("expected dietary note")
	}
	if DietaryNotes("Please pick us up at the station") != "" {
		t.Error("pickup request is not dietary")
	}
	if JoinNotes("", " a ", "b") != "a; b" {
		t.Error("JoinNotes")
	}
}

func TestParseDate(t *testing.T) {
	if _, ok := ParseDate("not a date"); ok {
		t.Error("should fail")
	}
	// 2026-11-05T20:00:00Z is already 6 Nov in IST.
	got, ok := ParseDate("2026-11-05T20:00:00Z")
	if !ok || FormatDate(got) != "2026-11-06" {
		t.Errorf("got %v", got)
	}
	if v, ok := ParseDate("2026-11-06"); !ok || FormatDate(v) != "2026-11-06" {
		t.Error("plain date")
	}
	if Number("12.5") != 12.5 || Number(3.0) != 3 || Number(nil) != 0 {
		t.Error("Number")
	}
}
