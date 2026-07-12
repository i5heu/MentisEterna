package task

import (
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestTaskPlugin(t *testing.T) {
	plugintest.Run(t, &TaskPlugin{}, plugintest.TestData{
		ValidPayload:   `{"status":"in_progress","difficulty":5,"fun":3,"priority":7,"description":"Write unit tests","due_date":"2025-01-15","time_estimation":"2h","time_used":"30m","recurring":"weekly","recurring_days":0,"completed_at":"","pending_does_not_force_daily_inclusion":true}`,
		InvalidPayload: `{"status":"invalid","difficulty":999,"fun":999}`,
	})
}
