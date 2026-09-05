package render

import (
	"os"
	"testing"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// TestMain : the tests compare palette labels and widths. The language is
// therefore fixed, otherwise the result would depend on $LANG.
func TestMain(m *testing.M) {
	i18n.Set("fr")
	os.Exit(m.Run())
}

// self7 : identity of the tests — id 7 on every network.
func self7(*model.Msg) int64 { return 7 }
