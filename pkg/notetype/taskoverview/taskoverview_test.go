package taskoverview

import (
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
	_ "github.com/i5heu/MentisEterna/pkg/notetype/task" // ensure task plugin registers its schema
)

func TestTaskOverviewPlugin(t *testing.T) {
	plugintest.Run(t, &TaskOverviewPlugin{}, plugintest.TestData{
		// Task overview has no config, but the harness still checks ViewBuilder and ActionHandler.
	})
}
