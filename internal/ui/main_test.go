package ui

import (
	"os"
	"testing"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// tgk : telegram chat key, where a test used to name a chat by its bare id.
func tgk(id int64) model.ChatKey { return model.ChatKey{Net: model.NetTelegram, ID: id} }

// TestMain : the tests compare labels and box widths. The language is
// therefore fixed, otherwise the result would depend on $LANG.
func TestMain(m *testing.M) {
	i18n.Set("fr")
	// A fold writes sidebar.toml under config.Dir(): the tests must never land
	// in the real configuration directory of whoever runs them.
	dir, err := os.MkdirTemp("", "ttyloom-ui-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("TTYLOOM_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
