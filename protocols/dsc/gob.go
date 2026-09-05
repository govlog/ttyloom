package dsc

import "encoding/gob"

// init registers the handles this backend hides behind the opaque fields of
// model.Chat (Peer, PhotoLoc), model.Media (Loc) and model.Msg (FromPhoto).
// Named local types, never arikawa ones: the gob cache stays free of the
// library, and a version bump of it cannot make an old cache unreadable.
type peer struct{ Channel, Guild uint64 } // model.Chat.Peer; Guild 0 = DM

type fileURL string // model.Media.Loc, model.Chat.PhotoLoc, model.Msg.FromPhoto

func init() {
	gob.Register(peer{})
	gob.Register(fileURL(""))
}
