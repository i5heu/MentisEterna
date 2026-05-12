package backup

import (
	"fmt"
	"sort"
	"testing"
	"time"
)

func TestParseBackupTime(t *testing.T) {
	key := "backups/mentis-2026-05-12T03-15-42.db.enc"
	ts, err := parseBackupTime(key)
	if err != nil {
		t.Fatalf("parseBackupTime: %v", err)
	}
	if ts.Year() != 2026 || ts.Month() != 5 || ts.Day() != 12 {
		t.Errorf("unexpected date: %v", ts)
	}
	if ts.Hour() != 3 || ts.Minute() != 15 || ts.Second() != 42 {
		t.Errorf("unexpected time: %v", ts)
	}
}

func TestParseBackupTimeInvalid(t *testing.T) {
	tests := []string{
		"not-a-backup.txt",
		"backups/mentis-2026-05-12.db.enc",
		"",
	}
	for _, key := range tests {
		if _, err := parseBackupTime(key); err == nil {
			t.Errorf("expected error for %q, got nil", key)
		}
	}
}

func TestClassifyBackupsEmpty(t *testing.T) {
	keep, del := ClassifyBackups(nil, time.Now(), DefaultRetentionPolicy())
	if len(keep) != 0 || len(del) != 0 {
		t.Error("expected both slices empty for nil input")
	}
}

func TestClassifyBackupsSkipsNonMatching(t *testing.T) {
	keys := []string{"some-random-file.txt", "another-one.log"}
	keep, del := ClassifyBackups(keys, time.Now(), DefaultRetentionPolicy())
	if len(keep) != 0 || len(del) != 0 {
		t.Errorf("expected non-matching keys to be skipped, got keep=%v del=%v", keep, del)
	}
}

// makeKey returns a backup S3 key for the given time.
func makeKey(t time.Time) string {
	return fmt.Sprintf("backups/mentis-%s.db.enc", t.UTC().Format("2006-01-02T15-04-05"))
}

func TestClassifyBackupsKeepLast7DaysMax3PerDay(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	// 4 backups per day for 14 days.
	var keys []string
	for d := 0; d < 14; d++ {
		for h := 0; h < 24; h += 6 {
			ts := now.Add(-time.Duration(d)*24*time.Hour - time.Duration(h)*time.Hour)
			keys = append(keys, makeKey(ts))
		}
	}

	keep, del := ClassifyBackups(keys, now, DefaultRetentionPolicy())

	// Within 7 days: max 3 per day. 7 days × 3 = 21. Older backups get
	// further thinned by weekly/monthly rules.
	keptPerDay := make(map[string]int)
	dayCutoff := now.AddDate(0, 0, -7)
	for _, k := range keep {
		ts, _ := parseBackupTime(k)
		if ts.After(dayCutoff) {
			keptPerDay[ts.Format("2006-01-02")]++
		}
	}
	for dk, count := range keptPerDay {
		if count > 3 {
			t.Errorf("day %s has %d backups kept (max 3): keep=%v del=%v", dk, count, keep, del)
		}
	}

	// At least some within-window backups should be deleted now (the 4th
	// per day).
	deletedWithinWindow := 0
	for _, k := range del {
		ts, _ := parseBackupTime(k)
		if ts.After(dayCutoff) {
			deletedWithinWindow++
		}
	}
	if deletedWithinWindow == 0 {
		t.Error("expected some backups within the 7-day window to be deleted (max 3/day)")
	}

	if len(keep)+len(del) != len(keys) {
		t.Errorf("keep(%d) + del(%d) != total(%d)", len(keep), len(del), len(keys))
	}
	t.Logf("keep=%d del=%d total=%d deletedWithin7d=%d", len(keep), len(del), len(keys), deletedWithinWindow)
}

func TestClassifyBackupsOnePerWeek(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	var keys []string
	for w := 0; w < 13; w++ {
		monday := now.AddDate(0, 0, -7*w)
		for _, offset := range []int{0, 2, 4} {
			ts := monday.Add(time.Duration(offset) * 24 * time.Hour)
			keys = append(keys, makeKey(ts))
		}
	}

	keep, _ := ClassifyBackups(keys, now, DefaultRetentionPolicy())

	dayCutoff := now.AddDate(0, 0, -7)
	weekCutoff := now.AddDate(0, -3, 0)

	keptPerWeek := make(map[string]int)
	for _, k := range keep {
		ts, _ := parseBackupTime(k)
		if ts.After(dayCutoff) {
			continue
		}
		if ts.After(weekCutoff) {
			year, week := ts.ISOWeek()
			wk := fmt.Sprintf("%d-W%02d", year, week)
			keptPerWeek[wk]++
		}
	}

	for wk, count := range keptPerWeek {
		if count > 1 {
			t.Errorf("week %s has %d backups kept (expected 1)", wk, count)
		}
	}
}

func TestClassifyBackupsOnePerMonth(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	var keys []string
	for m := 0; m < 60; m++ {
		midMonth := now.AddDate(0, -m, 0)
		keys = append(keys, makeKey(midMonth))
		keys = append(keys, makeKey(midMonth.Add(1*time.Hour)))
	}

	keep, _ := ClassifyBackups(keys, now, DefaultRetentionPolicy())

	monthCutoff := now.AddDate(-5, 0, 0)
	weekCutoff := now.AddDate(0, -3, 0)
	dayCutoff := now.AddDate(0, 0, -7)

	keptPerMonth := make(map[string]int)
	for _, k := range keep {
		ts, _ := parseBackupTime(k)
		if ts.After(dayCutoff) || ts.After(weekCutoff) {
			continue
		}
		if ts.After(monthCutoff) {
			mk := ts.Format("2006-01")
			keptPerMonth[mk]++
		}
	}

	for mk, count := range keptPerMonth {
		if count > 1 {
			t.Errorf("month %s has %d backups kept (expected 1)", mk, count)
		}
	}
}

func TestClassifyBackupsDeleteBeyond5Years(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	oldKeys := []string{
		makeKey(time.Date(2020, 1, 15, 0, 0, 0, 0, time.UTC)),
		makeKey(time.Date(2019, 6, 1, 0, 0, 0, 0, time.UTC)),
		makeKey(time.Date(2021, 5, 30, 12, 0, 0, 0, time.UTC)),
	}

	keys := append([]string{
		makeKey(now.Add(-1 * time.Hour)),
	}, oldKeys...)

	keep, _ := ClassifyBackups(keys, now, DefaultRetentionPolicy())

	for _, old := range oldKeys {
		if contains(keep, old) {
			ts, _ := parseBackupTime(old)
			t.Errorf("backup at %v (>5 years) was kept: %s", ts, old)
		}
	}

	if !contains(keep, keys[0]) {
		t.Error("recent backup was deleted")
	}
}

func TestClassifyBackupsNewestPerBucket(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	older := makeKey(time.Date(2026, 5, 28, 1, 0, 0, 0, time.UTC))
	newer := makeKey(time.Date(2026, 5, 28, 13, 0, 0, 0, time.UTC))

	keep, del := ClassifyBackups([]string{older, newer}, now, DefaultRetentionPolicy())

	if !contains(keep, newer) {
		t.Errorf("newer backup was NOT kept: %s", newer)
	}
	if contains(keep, older) {
		if !contains(del, older) {
			t.Errorf("older backup was kept but shouldn't have been: kept=%v del=%v", keep, del)
		}
	}

	old1 := makeKey(time.Date(2025, 3, 5, 0, 0, 0, 0, time.UTC))
	old2 := makeKey(time.Date(2025, 3, 25, 0, 0, 0, 0, time.UTC))

	keep2, _ := ClassifyBackups([]string{old1, old2, makeKey(now)}, now, DefaultRetentionPolicy())

	if !contains(keep2, old2) {
		t.Errorf("newer monthly backup was NOT kept: %s", old2)
	}
	if contains(keep2, old1) {
		t.Errorf("older monthly backup was kept (expected deletion): %s", old1)
	}
}

func TestClassifyBackupsBoundaries(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	// weekCutoff = March 15 12:00. After(weekCutoff) means strictly later.
	atWeekCutoff := makeKey(now.AddDate(0, -3, 0))                       // Mar 15 12:00 -> NOT after
	justBeforeWeek := makeKey(now.AddDate(0, -3, 0).Add(-1 * time.Hour)) // Mar 15 11:00
	justAfterWeek := makeKey(now.AddDate(0, -3, 0).Add(1 * time.Hour))   // Mar 15 13:00 -> after

	keep, del := ClassifyBackups([]string{atWeekCutoff, justBeforeWeek, justAfterWeek}, now, DefaultRetentionPolicy())

	// Both atWeekCutoff and justBeforeWeek fall into monthly bucket (March 2026).
	// atWeekCutoff is newer -> kept; justBeforeWeek -> deleted.
	if !contains(keep, atWeekCutoff) {
		t.Error("atWeekCutoff not kept")
	}
	if !contains(del, justBeforeWeek) {
		t.Errorf("justBeforeWeek should be deleted: keep=%v del=%v", keep, del)
	}
	if !contains(keep, justAfterWeek) {
		t.Error("justAfterWeek not kept")
	}

	// monthCutoff = June 15 2021 12:00.
	monthCutoff := now.AddDate(-5, 0, 0)
	atMonth := makeKey(monthCutoff)                        // Jun 15 2021 12:00 -> NOT after
	justBefore := makeKey(monthCutoff.Add(-1 * time.Hour)) // Jun 15 2021 11:00
	justAfter := makeKey(monthCutoff.Add(1 * time.Hour))   // Jun 15 2021 13:00 -> after

	keep2, del2 := ClassifyBackups([]string{atMonth, justBefore, justAfter}, now, DefaultRetentionPolicy())

	if !contains(del2, atMonth) {
		t.Errorf("atMonthCutoff should be deleted: keep=%v del=%v", keep2, del2)
	}
	if !contains(del2, justBefore) {
		t.Errorf("justBeforeMonth should be deleted: keep=%v del=%v", keep2, del2)
	}
	// justAfter is in monthly bucket (June 2021) -> kept.
	if !contains(keep2, justAfter) {
		t.Errorf("justAfterMonth should be kept: keep=%v del=%v", keep2, del2)
	}
}

func TestDefaultRetentionPolicy(t *testing.T) {
	p := DefaultRetentionPolicy()
	if p.KeepLastDays != 7 {
		t.Errorf("KeepLastDays=%d, want 7", p.KeepLastDays)
	}
	if p.WeeklyMonths != 3 {
		t.Errorf("WeeklyMonths=%d, want 3", p.WeeklyMonths)
	}
	if p.MonthlyYears != 5 {
		t.Errorf("MonthlyYears=%d, want 5", p.MonthlyYears)
	}
}

func TestClassifyBackupsSorting(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	var keys []string
	for d := 0; d < 30; d++ {
		keys = append(keys, makeKey(now.AddDate(0, 0, -d)))
	}

	keep1, del1 := ClassifyBackups(keys, now, DefaultRetentionPolicy())
	keep2, del2 := ClassifyBackups(keys, now, DefaultRetentionPolicy())

	sort.Strings(keep1)
	sort.Strings(keep2)
	sort.Strings(del1)
	sort.Strings(del2)

	if !stringSlicesEqual(keep1, keep2) {
		t.Error("keep slices differ between runs")
	}
	if !stringSlicesEqual(del1, del2) {
		t.Error("delete slices differ between runs")
	}
}

// --- helpers ---

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
