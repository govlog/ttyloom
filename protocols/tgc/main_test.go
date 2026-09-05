package tgc

import (
	"os"
	"testing"

	"github.com/govlog/ttyloom/internal/i18n"
)

// TestMain : the tests compare texts that are shown. The language is
// therefore fixed, otherwise the result would depend on $LANG.
func TestMain(m *testing.M) {
	i18n.Set("fr")
	os.Exit(m.Run())
}
