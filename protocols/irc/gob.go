package irc

import "encoding/gob"

// Handles this backend hides behind the opaque fields of model.Chat (Peer)
// and model.Media (Loc). Named local types: the gob cache stays readable
// across versions of the library.

// peer : model.Chat.Peer — the channel or nick the chat stands for, as the
// server spells it.
type peer struct{ Name string }

// ServerName gives the UI the name the chat id comes from (model.ChatIDer):
// the UI cleans the title of the chat for display.
func (p peer) ServerName() string { return p.Name }

// dccOffer : model.Media.Loc of an incoming DCC SEND, everything needed to
// fetch the file later. Port 0 with a Token is a reverse (passive) offer: the
// receiver listens and sends the port back.
type dccOffer struct {
	Nick  string
	Name  string
	IP    string
	Port  int
	Size  int64
	Token string
}

func init() {
	gob.Register(peer{})
	gob.Register(dccOffer{})
}
