package rss

import (
	"context"
	"testing"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

// evalViewFixture builds a reader with three classified items: a high-points
// evaluated one ("high"), a low-points evaluated one ("low"), and an
// unevaluated legacy one ("legacy").
func evalViewFixture(t *testing.T, body string) (*Plugin, *db.DB, int64) {
	t.Helper()
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)
	noteID := plugintest.CreateNote(t, d, "Reader", p)
	setNoteBody(t, d, noteID, body)

	tx, _ := d.Begin()
	if err := p.SaveConfig(context.Background(), tx, 0, noteID,
		jsonRaw(`{"feeds":[{"url":"https://a.example/rss","name":"A","enabled":true}]}`)); err != nil {
		tx.Rollback()
		t.Fatalf("SaveConfig: %v", err)
	}
	tx.Commit()

	var feedID int64
	if err := d.QueryRow(`SELECT id FROM ct_rss_feeds WHERE note_id = ?`, noteID).Scan(&feedID); err != nil {
		t.Fatalf("load feed: %v", err)
	}
	seed := func(key string, sig, pts float64, evaluated int) {
		t.Helper()
		if _, err := d.Exec(
			`INSERT INTO ct_rss_items
			 (feed_id, item_key, title, fetched_at, feed_content, classify_status,
			  significance, points, evaluated, reflection)
			 VALUES (?, ?, ?, ?, ?, 'done', ?, ?, ?, 'note')`,
			feedID, key, key, nowStr(), "content", sig, pts, evaluated,
		); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	seed("high", 0.8, 7.5, 1)
	seed("low", 0.2, 1.0, 1)
	seed("legacy", 0, 0, 0)
	return p, d, noteID
}

func TestViewMinPointsRejection(t *testing.T) {
	// Default floor (defaultMinPoints): evaluated low-scorer rejected,
	// high-scorer and unevaluated item stay.
	p, d, noteID := evalViewFixture(t, "")

	view, err := p.BuildView(context.Background(), d.DB, 0, noteID)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}
	v := view.(*View)
	if len(v.Items) != 2 || len(v.Rejected) != 1 {
		t.Fatalf("items=%d rejected=%d, want 2/1", len(v.Items), len(v.Rejected))
	}
	if v.Rejected[0].Title != "low" {
		t.Errorf("rejected item = %q, want low", v.Rejected[0].Title)
	}
	if v.Rejected[0].Points != 1.0 || !v.Rejected[0].Evaluated {
		t.Errorf("rejected evaluation missing: %+v", v.Rejected[0])
	}
	if v.Counts.Rejected != 1 {
		t.Errorf("counts.rejected = %d", v.Counts.Rejected)
	}
	// Evaluation coverage counts: two evaluated, one legacy unevaluated.
	if v.Counts.Evaluated != 2 || v.Counts.Unevaluated != 1 {
		t.Errorf("counts = evaluated %d, unevaluated %d; want 2/1", v.Counts.Evaluated, v.Counts.Unevaluated)
	}
	// Evaluation fields round-trip into the view payload.
	var high *ItemView
	for i := range v.Items {
		if v.Items[i].Title == "high" {
			high = &v.Items[i]
		}
	}
	if high == nil || high.Significance != 0.8 || high.Points != 7.5 || !high.Evaluated {
		t.Errorf("high item evaluation wrong: %+v", high)
	}

	// Disabling the floor via body rule: everything is shown again.
	p, d, noteID = evalViewFixture(t, "min_points: 0\n")
	view, err = p.BuildView(context.Background(), d.DB, 0, noteID)
	if err != nil {
		t.Fatalf("BuildView (disabled): %v", err)
	}
	v = view.(*View)
	if len(v.Items) != 3 || len(v.Rejected) != 0 {
		t.Errorf("disabled floor: items=%d rejected=%d, want 3/0", len(v.Items), len(v.Rejected))
	}

	// Raising the floor rejects the low scorer; the high scorer (7.5 ≥ 6)
	// and the unevaluated item stay.
	p, d, noteID = evalViewFixture(t, "min_points: 6\n")
	view, err = p.BuildView(context.Background(), d.DB, 0, noteID)
	if err != nil {
		t.Fatalf("BuildView (raised): %v", err)
	}
	v = view.(*View)
	if len(v.Items) != 2 || len(v.Rejected) != 1 {
		t.Errorf("raised floor: items=%d rejected=%d, want 2/1", len(v.Items), len(v.Rejected))
	}
}
