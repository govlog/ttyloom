//go:build nospell

package spell

import (
	"errors"

	"github.com/govlog/ttyloom/internal/i18n"
)

// Stub of the -tags nospell build: no cgo, no hunspell. New always fails and
// the UI keeps spell off.
type Checker struct{}

func New(mode, perso string) (*Checker, error) {
	return nil, errors.New(i18n.T("spell_nospell"))
}
func (c *Checker) Check(string) bool       { return true }
func (c *Checker) Suggest(string) []string { return nil }
func (c *Checker) Ignore(string)           {}
func (c *Checker) AddPersist(string) error { return nil }
