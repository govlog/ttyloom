package hook

import (
	"os"
	"testing"
)

// TestMain : a run starts in config.Dir(); the tests give it a directory of
// their own, never the real one (and one that exists on a CI runner).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ttyloom-hook-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("TTYLOOM_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
