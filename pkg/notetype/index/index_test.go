package index_test

import (
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/index"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestIndexPlugin(t *testing.T) {
	plugintest.Run(t, &index.IndexPlugin{}, plugintest.TestData{
		ValidPayload:   `{"mode":"global","selected_tags":["test"]}`,
		InvalidPayload: `{"mode":"invalid"}`,
	})
}
