package home

import (
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestHomePlugin(t *testing.T) {
	plugintest.Run(t, &HomePlugin{}, plugintest.TestData{
		// Home has no config, but the harness checks ViewBuilder and ActionHandler.
	})
}
