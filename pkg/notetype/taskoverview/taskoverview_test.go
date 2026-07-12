package taskoverview

import (
	"testing"

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
