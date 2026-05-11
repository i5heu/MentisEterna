package example

import (
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestExamplePlugin(t *testing.T) {
	plugintest.Run(t, &ExamplePlugin{}, plugintest.TestData{
		ValidPayload:   `{"items":[{"label":"Buy milk","checked":false},{"label":"Walk dog","checked":true}]}`,
		InvalidPayload: `{"items":[{"label":"","checked":false}]}`,
	})
}
