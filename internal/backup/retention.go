package backup

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	backupPrefix = "backups/mentis-"
	backupSuffix = ".db.enc"
)

// parseBackupTime extracts the UTC timestamp from a backup S3 key.
// Keys are expected in the format: backups/mentis-YYYY-MM-DDTHH-MM-SS.db.enc
func parseBackupTime(key string) (time.Time, error) {
	s := strings.TrimPrefix(key, backupPrefix)
	s = strings.TrimSuffix(s, backupSuffix)
	t, err := time.Parse("2006-01-02T15-04-05", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse backup time from %q: %w", key, err)
	}
	return t, nil
}

// RetentionPolicy defines the retention windows for backup cleanup.
type RetentionPolicy struct {
	KeepLastDays int // Keep ALL backups from the last N days
	WeeklyMonths int // Keep 1 per ISO week for N months
	MonthlyYears int // Keep 1 per calendar month for N years
}

// DefaultRetentionPolicy returns the standard policy:
//
//	Last 7 days:   keep all backups
//	7d – 3 months: keep 1 per week (newest in each ISO week)
//	3m – 5 years:  keep 1 per month (newest in each calendar month)
//	Older than 5y: delete all
func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{
		KeepLastDays: 7,
		WeeklyMonths: 3,
		MonthlyYears: 5,
	}
}

type backupEntry struct {
	key string
	t   time.Time
}

// ClassifyBackups takes unsorted backup keys and a reference time (usually time.Now()),
// and returns two slices: keys to keep and keys to delete.
//
// Algorithm (processes newest-first, greedily keeping the newest in each bucket):
//  1. Backups within KeepLastDays → keep all
//  2. Backups within WeeklyMonths → keep newest per ISO week
//  3. Backups within MonthlyYears → keep newest per calendar month
//  4. Everything older → delete
//
// Keys that don't match the expected naming pattern are silently skipped
// (they won't be deleted).
func ClassifyBackups(keys []string, now time.Time, policy RetentionPolicy) (keep, delete []string) {
	var backups []backupEntry
	for _, k := range keys {
		t, err := parseBackupTime(k)
		if err != nil {
			// Skip keys that don't match our naming pattern — they're
			// not ours to delete.
			continue
		}
		backups = append(backups, backupEntry{key: k, t: t})
	}

	if len(backups) == 0 {
		return nil, nil
	}

	// Sort newest first.
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].t.After(backups[j].t)
	})

	keepSet := make(map[string]bool, len(backups))
	keptWeeks := make(map[string]bool)  // "2026-W20"
	keptMonths := make(map[string]bool) // "2026-05"

	dayCutoff := now.AddDate(0, 0, -policy.KeepLastDays)
	weekCutoff := now.AddDate(0, -policy.WeeklyMonths, 0)
	monthCutoff := now.AddDate(-policy.MonthlyYears, 0, 0)

	for _, b := range backups {
		// Rule 1: Within the last N days → keep every backup.
		if b.t.After(dayCutoff) {
			keepSet[b.key] = true
			continue
		}

		// Rule 2: Within N months → keep 1 per ISO week (the newest, since
		// we iterate newest-first).
		if b.t.After(weekCutoff) {
			year, week := b.t.ISOWeek()
			wk := fmt.Sprintf("%d-W%02d", year, week)
			if !keptWeeks[wk] {
				keptWeeks[wk] = true
				keepSet[b.key] = true
			}
			continue
		}

		// Rule 3: Within N years → keep 1 per calendar month (newest).
		if b.t.After(monthCutoff) {
			mk := b.t.Format("2006-01")
			if !keptMonths[mk] {
				keptMonths[mk] = true
				keepSet[b.key] = true
			}
			continue
		}

		// Rule 4: Older than N years → delete.
	}

	for _, b := range backups {
		if keepSet[b.key] {
			keep = append(keep, b.key)
		} else {
			delete = append(delete, b.key)
		}
	}

	return keep, delete
}
