package tgc

import (
	"encoding/gob"

	"github.com/gotd/td/tg"
)

// init registers the concrete types this backend hides behind the opaque
// handles of model.Chat (Peer, PhotoLoc), model.Media (Loc) and model.Msg
// (FromPhoto). Without it, the gob cache refuses to encode and decode them.
// The registration only has to live in a package the binary imports: main
// pulls tgc in, and the cache no longer knows a thing about Telegram.
func init() {
	for _, v := range []any{
		// tg.InputFileLocationClass (model.Media.Loc).
		&tg.InputPhotoFileLocation{},
		&tg.InputDocumentFileLocation{},
		// model.Chat.PhotoLoc / model.Msg.FromPhoto.
		&tg.InputPeerPhotoFileLocation{},
		// tg.InputPeerClass (model.Chat.Peer). InputPeerSelf: the
		// "Saved Messages" chat (peers.User.InputPeer when u.Self()).
		&tg.InputPeerUser{},
		&tg.InputPeerChat{},
		&tg.InputPeerChannel{},
		&tg.InputPeerSelf{},
	} {
		gob.Register(v)
	}
}
