package taskoverview

import (
	"strings"
	"testing"

	internaldb "github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
	_ "github.com/i5heu/MentisEterna/pkg/notetype/task" // ensure task plugin registers its schema
)

func TestTaskOverviewPlugin(t *testing.T) {
	plugintest.Run(t, &TaskOverviewPlugin{}, plugintest.TestData{
		ValidPayload: `{
			"daily_task_count": 4,
			"urgent_due_days": 2,
			"priority_weight": 5,
			"due_urgency_weight": 7,
			"difficulty_weight": -1,
			"fun_weight": 0.5,
			"time_estimation_weight": -0.25,
			"fun_time_weight": 0.2
		}`,
		InvalidPayload: `{"daily_task_count":0,"priority_weight":1000}`,
	})
}

func TestTaskOverviewPlugin_InitSchemaMigratesLegacyDailyTable(t *testing.T) {
	t.Parallel()

	d, err := internaldb.OpenInMemory()
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer d.Close()

	if _, err := d.Exec(`DROP TABLE IF EXISTS ct_taskoverview_daily`); err != nil {
		t.Fatalf("drop ct_taskoverview_daily: %v", err)
	}
	if _, err := d.Exec(`DROP TABLE IF EXISTS ct_taskoverview_config`); err != nil {
		t.Fatalf("drop ct_taskoverview_config: %v", err)
	}

	if _, err := d.Exec(`
		CREATE TABLE ct_taskoverview_daily (
			overview_note_id INTEGER NOT NULL,
			task_note_id     INTEGER NOT NULL,
			assigned_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			FOREIGN KEY (overview_note_id) REFERENCES notes(id) ON DELETE CASCADE,
			FOREIGN KEY (task_note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
	`); err != nil {
		t.Fatalf("create legacy ct_taskoverview_daily: %v", err)
	}
	if _, err := d.Exec(`CREATE TABLE ct_taskoverview_config (note_id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create legacy ct_taskoverview_config: %v", err)
	}

	plugin := &TaskOverviewPlugin{}
	if err := plugin.InitSchema(d.DB); err != nil {
		t.Fatalf("InitSchema legacy migration: %v", err)
	}

	rows, err := d.Query(`PRAGMA table_info(ct_taskoverview_daily)`)
	if err != nil {
		t.Fatalf("pragma daily table info: %v", err)
	}
	defer rows.Close()

	cols := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan daily table info: %v", err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate daily table info: %v", err)
	}
	if !cols["generation_id"] {
		t.Fatal("generation_id column missing after legacy migration")
	}
	if !cols["position"] {
		t.Fatal("position column missing after legacy migration")
	}

	indexRows, err := d.Query(`PRAGMA index_list(ct_taskoverview_daily)`)
	if err != nil {
		t.Fatalf("pragma daily index list: %v", err)
	}
	defer indexRows.Close()

	seenGenerationIndex := false
	for indexRows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := indexRows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan daily index list: %v", err)
		}
		if strings.Contains(name, "generation") {
			seenGenerationIndex = true
		}
	}
	if err := indexRows.Err(); err != nil {
		t.Fatalf("iterate daily index list: %v", err)
	}
	if !seenGenerationIndex {
		t.Fatal("generation index missing after legacy migration")
	}
}
