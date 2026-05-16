package print

import (
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestPrintPlugin(t *testing.T) {
	plugintest.Run(t, &PrintPlugin{}, plugintest.TestData{
		ValidPayload:   `{"target_note_id":42}`,
		InvalidPayload: `{"target_note_id":-1}`,
	})
}
