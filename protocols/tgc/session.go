package tgc

import (
	"context"

	"github.com/gotd/td/session"

	"github.com/govlog/ttyloom/internal/config"
)

// Keep gotd's session reader and replace its truncating write with a rename.
type sessionFile struct{ session.FileStorage }

func (s *sessionFile) StoreSession(_ context.Context, data []byte) error {
	return config.WriteAtomic(s.Path, data, 0o600)
}
