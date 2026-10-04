package dsc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/utils/httputil/httpdriver"
	"github.com/gorilla/websocket"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// The backend contract holds at compile time: every method of model.Backend
// is there.
var _ model.Backend = (*Client)(nil)

// Discord has reactions, edits and deletes, nothing else of what the UI gates —
// leaving a room and blocking included, so the context menu offers neither.
func TestCaps(t *testing.T) {
	got := New(Config{}, make(chan model.Event, 1)).Caps()
	want := model.Caps{Reactions: true, AnyReaction: true, Edit: true, Delete: true, Gifs: true, Search: true, GlobalSearch: true, History: true}
	if got != want {
		t.Fatalf("Caps: %+v, want %+v", got, want)
	}
}

// evOf runs one call of the backend and gives back the event it posts. The
// client has no state at all: a method tested here must never touch it.
func evOf(t *testing.T, call func(*Client)) model.Event {
	t.Helper()
	ev := make(chan model.Event, 4)
	call(&Client{Poster: model.Poster{Events: ev}})
	select {
	case e := <-ev:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
	return nil
}

// Every method Discord cannot do answers something. The UI gates them off
// with Caps, but a call must never leave a window waiting for an answer, and
// must never do half of a destructive action.
func TestUnsupported(t *testing.T) {
	ctx := context.Background()
	no := i18n.T("net_unsupported", Net)
	warn := func(name string) model.Event {
		return model.EvLog{Level: "WARN", Msg: "discord: " + name + " not supported"}
	}
	chat := &model.Chat{ID: 5, Peer: peer{Channel: 5}}
	md := &model.Media{}
	for _, tc := range []struct {
		name string
		call func(*Client)
		want model.Event
	}{
		{"SearchContacts", func(c *Client) { c.SearchContacts(ctx, "q", 10) },
			model.EvContactsFound{Query: "q", Err: no}},
		{"Contacts", func(c *Client) { c.Contacts(ctx) }, model.EvContacts{Err: no}},
		{"Resolve", func(c *Client) { c.Resolve(ctx, "q", false, 0) }, model.EvChat{Query: "q", Err: no}},
		{"Whois", func(c *Client) { c.Whois(ctx, chat) }, model.EvWhois{ChatID: 5, Err: no}},
		{"WhoisMember", func(c *Client) { c.WhoisMember(ctx, "@bob") }, model.EvWhois{Err: no}},
		{"WhoRead", func(c *Client) { c.WhoRead(ctx, chat, 3, 0) },
			model.EvWho{ChatID: 5, ID: 3, Text: no}},
		{"DownloadMap", func(c *Client) { c.DownloadMap(ctx, md, "/x") },
			model.EvDownloaded{Media: md, Err: no}},
		{"Block", func(c *Client) { c.Block(ctx, chat) }, warn("Block")},
		{"BlockMember", func(c *Client) { c.BlockMember(ctx, "@bob") }, warn("BlockMember")},
		{"Leave", func(c *Client) { c.Leave(ctx, chat) }, warn("Leave")},
	} {
		if got := evOf(t, tc.call); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s = %#v, want %#v", tc.name, got, tc.want)
		}
	}
}

// toggleReaction decides what one React call does: a reaction the account
// already set goes away, any other one is added. React("") means "drop mine",
// the UI counts one reaction per message (Telegram) and the emoji is then
// read from the message itself.
func TestToggleReaction(t *testing.T) {
	m := &discord.Message{Reactions: []discord.Reaction{
		{Emoji: discord.Emoji{Name: "🔥"}, Me: true},
		{Emoji: discord.Emoji{Name: "👍"}},
	}}
	for _, tc := range []struct {
		in    string
		msg   *discord.Message
		emoji string
		add   bool
	}{
		{"🔥", m, "🔥", false},                // already mine: it goes
		{"👍", m, "👍", true},                 // set by somebody else: added
		{"🎉", m, "🎉", true},                 // nobody has it
		{"", m, "🔥", false},                 // drop mine, whichever it is
		{"", &discord.Message{}, "", false}, // nothing of mine to drop
		{"🔥", nil, "🔥", true},               // message not in the cache: added
	} {
		e, add := toggleReaction(tc.msg, tc.in)
		if e != tc.emoji || add != tc.add {
			t.Errorf("toggleReaction(%q) = %q %v, want %q %v", tc.in, e, add, tc.emoji, tc.add)
		}
	}
}

// A custom emoji reaches the backend as ":name:", the REST route wants
// "name:id": the reaction already on the message carries that id.
func TestAPIEmoji(t *testing.T) {
	c := &Client{}
	m := &discord.Message{Reactions: []discord.Reaction{{Emoji: discord.Emoji{ID: 88, Name: "party"}}}}
	if e, ok := c.apiEmoji(":party:", m, 0); !ok || string(e) != "party:88" {
		t.Fatalf("custom emoji = %q %v, want %q true", e, ok, "party:88")
	}
	if e, ok := c.apiEmoji("🔥", m, 0); !ok || string(e) != "🔥" {
		t.Fatalf("unicode emoji = %q %v, want %q true", e, ok, "🔥")
	}
	// Not on the message and no guild to look into: nothing to send, and no
	// state read (c.st is nil here).
	if _, ok := c.apiEmoji(":nope:", m, 0); ok {
		t.Fatal("unknown custom emoji: resolved all the same")
	}
}

// Discord answers a history page newest first; the window reads it the other
// way round, so the conversion turns it around.
func TestMsgsOfAscending(t *testing.T) {
	c := &Client{}
	got := c.msgsOf([]discord.Message{
		{ID: msgID(3000), Author: discord.User{ID: 1}},
		{ID: msgID(2000), Author: discord.User{ID: 1}},
		{ID: msgID(1000), Author: discord.User{ID: 1}},
	})
	want := []int{int(msgID(1000)), int(msgID(2000)), int(msgID(3000))}
	ids := make([]int, 0, len(got))
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("history order = %v, want %v", ids, want)
	}
}

// A guild channel is not ours to delete: DeleteChannel on one, with the right
// permission, would drop it for everybody. The type of the channel decides,
// and a handle made outside the cache carries no guild id at all — so the
// refusal must hold on such a handle too.
func TestDeleteChatRefusesGuildChannel(t *testing.T) {
	c := dmState(t)
	ev := make(chan model.Event, 4)
	c.Poster = model.Poster{Events: ev}
	ch := &discord.Channel{ID: 8, GuildID: 9, Type: discord.GuildText, Name: "general"}
	if err := c.state().Cabinet.ChannelSet(ch, false); err != nil {
		t.Fatalf("channel of the test: %s", err)
	}
	for _, chat := range []*model.Chat{
		{ID: 8, Peer: peer{Channel: 8, Guild: 9}},
		{ID: 8, Peer: peer{Channel: 8}}, // handle made outside the cache: no guild id
		{ID: 9, Peer: peer{Channel: 9}}, // channel nobody knows: refused as well
	} {
		c.DeleteChat(context.Background(), chat)
		got := <-ev
		want := model.EvLog{Level: "WARN", Msg: "discord: DeleteChat not supported"}
		if got != want {
			t.Fatalf("DeleteChat(%v) = %#v, want %#v", chat.Peer, got, want)
		}
	}
}

// A history page has to land in the store: outside the gateway it is the only
// thing that fills it, and acking, dropping one's own reaction and the ids of
// the custom emojis all read from there.
func TestHistorySeedsCabinet(t *testing.T) {
	c := dmState(t)
	ev := make(chan model.Event, 4)
	c.Poster = model.Poster{Events: ev}
	// A REST message carries no guild_id: without the guild of the chat put
	// back on it, the nickname of the member is lost — on the page shown and
	// on the store copy an edit or a reaction re-converts later.
	if err := c.state().Cabinet.MemberSet(9,
		&discord.Member{User: discord.User{ID: 42, Username: "bob"}, Nick: "Bobby"}, false); err != nil {
		t.Fatalf("member of the test: %s", err)
	}
	page := []discord.Message{
		{ID: msgID(3000), ChannelID: 8, Author: discord.User{ID: 42, Username: "bob"}},
		{ID: msgID(2000), ChannelID: 8, Author: discord.User{ID: 42, Username: "bob"}},
	}
	c.history(context.Background(), "test", &model.Chat{ID: 8, Peer: peer{Channel: 8, Guild: 9}},
		model.EvHistory{ChatID: 8}, 50, 0,
		func(*api.Client, discord.ChannelID) ([]discord.Message, error) { return page, nil })
	got, ok := (<-ev).(model.EvHistory)
	if !ok || len(got.Msgs) != 2 || got.Msgs[0].ID != int(msgID(2000)) || !got.Done {
		t.Fatalf("history = %#v", got)
	}
	if got.Msgs[0].From != "Bobby" {
		t.Fatalf("author of the page = %q, want the guild nickname", got.Msgs[0].From)
	}
	for _, id := range []discord.MessageID{msgID(2000), msgID(3000)} {
		m, err := c.state().Cabinet.Message(8, id)
		if err != nil {
			t.Fatalf("message %d absent from the store: %s", id, err)
		}
		if m.GuildID != 9 {
			t.Fatalf("message %d stored with guild %d, want 9", id, m.GuildID)
		}
	}
}

// A page newer than all the store holds — the reload after a reconnection —
// goes into the store whole, even a full one: it only adds a message newer
// than its first, so a page given newest first kept its newest message alone.
func TestHistorySeedsNewerPage(t *testing.T) {
	ev := make(chan model.Event, 4)
	c := testClient(ev)
	cab := c.state().Cabinet
	for i := range int64(100) { // a full store
		if err := cab.MessageSet(&discord.Message{ID: msgID(1000 + i), ChannelID: 8}, false); err != nil {
			t.Fatal(err)
		}
	}
	var page []discord.Message
	for i := int64(1105); i >= 1100; i-- { // newest first, like the REST answer
		page = append(page, discord.Message{ID: msgID(i), ChannelID: 8})
	}
	c.history(context.Background(), "test", &model.Chat{ID: 8, Peer: peer{Channel: 8}}, model.EvHistory{ChatID: 8}, 50, 0,
		func(*api.Client, discord.ChannelID) ([]discord.Message, error) { return page, nil })
	next(t, ev)
	for _, m := range page {
		if _, err := cab.Message(8, m.ID); err != nil {
			t.Fatalf("message %d of the page not in the store: %s", m.ID, err)
		}
	}
}

// A sync page comes newest first and stops at the first message the window
// already holds — a hole can then only be at the far end of the past.
func TestNewerThan(t *testing.T) {
	page := []discord.Message{{ID: msgID(3000)}, {ID: msgID(2000)}, {ID: msgID(1000)}}
	if got := newerThan(page, int(msgID(2000))); len(got) != 1 || got[0].ID != msgID(3000) {
		t.Fatalf("newerThan = %v, want the 3000 alone", got)
	}
	if got := newerThan(page, 0); len(got) != 3 {
		t.Fatalf("newerThan(0) = %d messages, want the whole page", len(got))
	}
	if got := newerThan(page, int(msgID(9000))); len(got) != 0 {
		t.Fatalf("newerThan of a known page = %d messages, want none", len(got))
	}
}

// LoadDialogs gives the presence the state already holds for every DM, after
// the list itself: the gateway only sends a PRESENCE_UPDATE when a status
// changes, so without that pass a correspondent already online at the start
// stays unmarked until they move.
func TestLoadDialogsPresence(t *testing.T) {
	c := dmState(t) // one DM, id 5, with bob (42) in it
	ev := make(chan model.Event, 8)
	c.Poster = model.Poster{Events: ev}
	cab := c.state().Cabinet
	// A second DM, nothing known of its recipient: no line for that one.
	if err := cab.ChannelSet(&discord.Channel{ID: 6, Type: discord.DirectMessage,
		DMRecipients: []discord.User{{ID: 43, Username: "carol"}}}, false); err != nil {
		t.Fatalf("second DM: %s", err)
	}
	// A guild holding a single category: the type filter drops it, and the
	// whole listing is then served by the cache alone — no REST call.
	if err := cab.GuildSet(&discord.Guild{ID: 9, Name: "g"}, false); err != nil {
		t.Fatalf("guild of the test: %s", err)
	}
	if err := cab.ChannelSet(&discord.Channel{ID: 8, GuildID: 9,
		Type: discord.GuildCategory}, false); err != nil {
		t.Fatalf("category of the test: %s", err)
	}
	if err := cab.PresenceSet(0, &discord.Presence{
		User: discord.User{ID: 42}, Status: discord.OnlineStatus}, false); err != nil {
		t.Fatalf("presence of the test: %s", err)
	}

	c.LoadDialogs(context.Background())
	// The list comes first: the chat has to exist before its presence lands.
	if d, ok := next(t, ev).(model.EvDialogs); !ok || len(d.Chats) != 2 {
		t.Fatalf("dialogs = %#v", d)
	}
	// Keyed by the id of the DM channel, like the PresenceUpdate handler: the
	// UI knows a presence by the chat it belongs to.
	want := model.EvPresence{UserID: 5, Status: i18n.T("presence_online")}
	if got := next(t, ev); got != want {
		t.Fatalf("presence = %#v, want %#v", got, want)
	}
	select {
	case got := <-ev:
		t.Fatalf("DM with no presence in the cache: %#v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

// A gateway closed for good — 4004, the token was revoked — ends Run with the
// error, and the token file of the QR login goes: the network says why it
// stopped, and /discord login shows a new QR instead of the dead token.
func TestRunEndsOnRevokedToken(t *testing.T) {
	for _, ready := range []bool{true, false} { // revoked during the session, or while ttyloom was closed
		revokedRun(t, ready)
	}
}

func revokedRun(t *testing.T, ready bool) {
	up := websocket.Upgrader{}
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v9/gateway", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"url":"ws%s/ws"}`, strings.TrimPrefix(srv.URL, "http"))
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		ws.WriteJSON(map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 41250}})
		var identify map[string]any
		ws.ReadJSON(&identify)
		if ready {
			ws.WriteJSON(map[string]any{"op": 0, "t": "READY", "s": 1, "d": map[string]any{
				"v": 9, "session_id": "s", "user": map[string]any{"id": "7", "username": "me"}}})
			time.Sleep(100 * time.Millisecond) // the client is up: the password changes on the phone
		}
		ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4004, "Authentication failed."))
		time.Sleep(100 * time.Millisecond)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()
	// The gateway address comes from a client of arikawa's own, on the
	// default transport.
	u, _ := url.Parse(srv.URL)
	old := http.DefaultTransport
	http.DefaultTransport = testCDNTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "discord.com" {
			r = r.Clone(r.Context())
			r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
		}
		return old.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = old })

	path := filepath.Join(t.TempDir(), "discord.token")
	if err := os.WriteFile(path, []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New(Config{Token: "tok", TokenFile: path}, make(chan model.Event, 64))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run ended with no error: the network would look stopped on purpose")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run still waiting on a gateway closed for good")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ready %v: token file of the QR login kept: %v", ready, err)
	}
}

// next : the next event posted, or a failure after two seconds.
func next(t *testing.T, ev <-chan model.Event) model.Event {
	t.Helper()
	select {
	case e := <-ev:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
	return nil
}

// Same contract as tgc: an upload answers on the tmpID of the pending line
// the UI has already shown. Missing file: nothing goes to the network.
func TestUploadReportsTmpID(t *testing.T) {
	ev := make(chan model.Event, 4)
	c := New(Config{}, ev)
	c.SendFile(context.Background(), &model.Chat{ID: 5}, filepath.Join(t.TempDir(), "absent.bin"), "", false, 42)
	select {
	case e := <-ev:
		s, ok := e.(model.EvSent)
		if !ok || s.ChatID != 5 || s.TmpID != 42 || s.Err == "" {
			t.Fatalf("event: %#v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
}

// The state is built by New, not by Run: a chat replayed from the disk cache
// exists before the network is up, and DeleteChat on it read c.st on the UI
// goroutine — nil until Run had run (c.state() now), a crash of the whole client.
func TestStateBuiltInNew(t *testing.T) {
	c := New(Config{}, make(chan model.Event, 4))
	if c.state() == nil {
		t.Fatal("state built in Run: a call before Run dereferences nil")
	}
	c.DeleteChat(context.Background(), &model.Chat{ID: 1, Peer: peer{Channel: 1}}) // must not panic
}

// fetch stops at the cap: the size the API declares is what gates the
// automatic downloads, and a body bigger than announced must not fill the disk.
func TestFetchCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(bytes.Repeat([]byte("x"), 100)) }))
	defer srv.Close()
	useTestCDN(t, srv.URL)
	path := filepath.Join(t.TempDir(), "f")
	if err := fetch(context.Background(), "https://cdn.discordapp.com/test", path, 50); err == nil {
		t.Fatal("body over the cap: no error")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("body over the cap: file kept")
	}
	if err := fetch(context.Background(), "https://cdn.discordapp.com/test", path, 100); err != nil {
		t.Fatalf("body at the cap: %v", err)
	}
}

// An attachment URL kept in the disk cache is signed for about a day: past
// its end (ex=) the CDN answers 404, and the download asks Discord to sign it
// again first.
func TestDownloadRefreshesExpiredURL(t *testing.T) {
	old := "https://cdn.discordapp.com/attachments/1/2/a.png?ex=5f5e1000&is=5f5d0000&hm=aa"
	fresh := "https://cdn.discordapp.com/attachments/1/2/a.png?ex=7fffffff&is=6a000000&hm=bb"
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("hm") != "bb" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("png"))
	}))
	defer cdn.Close()
	useTestCDN(t, cdn.URL)
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			URLs []string `json:"attachment_urls"`
		}
		if r.URL.Path != "/api/v9/attachments/refresh-urls" || json.NewDecoder(r.Body).Decode(&body) != nil ||
			len(body.URLs) != 1 || body.URLs[0] != old {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"refreshed_urls": []map[string]string{{"original": old, "refreshed": fresh}}})
	}))
	defer rest.Close()
	ev := make(chan model.Event, 4)
	c := New(Config{}, ev)
	useTestAPI(t, c, rest)
	path := filepath.Join(t.TempDir(), "a.png")
	c.Download(context.Background(), &model.Media{Loc: fileURL(old), Size: 3}, path)
	if e := next(t, ev).(model.EvDownloaded); e.Err != "" || e.Path != path {
		t.Fatalf("download of an expired URL: %+v", e)
	}
}

// A download is cut when it stops moving, never because it is long: a video
// on a slow link takes minutes, a stalled connection frees its slot.
func TestFetchIdleNotTotal(t *testing.T) {
	old := dlIdle
	dlIdle = 200 * time.Millisecond
	t.Cleanup(func() { dlIdle = old })
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := range 12 { // 600 ms of transfer, a piece every 50 ms
			if r.URL.Path == "/stall" && i == 1 {
				select { // one piece, then nothing until the client gives up
				case <-r.Context().Done():
				case <-stop:
				}
				return
			}
			w.Write(bytes.Repeat([]byte("x"), 1024))
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer srv.Close()
	defer close(stop)
	useTestCDN(t, srv.URL)
	path := filepath.Join(t.TempDir(), "f")
	if err := fetch(context.Background(), "https://cdn.discordapp.com/slow", path, 1<<20); err != nil {
		t.Fatalf("transfer longer than the idle delay, still moving: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- fetch(context.Background(), "https://cdn.discordapp.com/stall", path+"2", 1<<20) }()
	select {
	case err := <-done:
		if !errors.Is(err, errStalled) {
			t.Fatalf("stalled transfer: %v, want the stalled error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled transfer not cut")
	}
}

type testCDNTransport func(*http.Request) (*http.Response, error)

func (f testCDNTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func useTestCDN(t *testing.T, target string) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	old := dlClient
	c := *old
	c.Transport = testCDNTransport(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
		return http.DefaultTransport.RoundTrip(r)
	})
	dlClient = &c
	t.Cleanup(func() { dlClient = old })
}

// useTestAPI sends the REST requests of c to srv, their paths kept.
func useTestAPI(t *testing.T, c *Client, srv *httptest.Server) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	c.state().Client.Client.Client = httpdriver.WrapClient(http.Client{Transport: testCDNTransport(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
		return http.DefaultTransport.RoundTrip(r)
	})})
}

// oldMessage : a server whose one message, with my 🔥 on it, is in no store:
// the page around it answers, the single-message route is for bots only.
// The requests it got come back on reqs.
func oldMessage(t *testing.T, reqs chan<- string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs <- r.Method + " " + r.URL.Path
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/5/messages"):
			json.NewEncoder(w).Encode([]discord.Message{{ID: msgID(900), ChannelID: 5,
				Reactions: []discord.Reaction{{Count: 1, Me: true, Emoji: discord.Emoji{Name: "🔥"}}}}})
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"message":"Only bots can use this endpoint","code":20002}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Dropping my reaction on a message the store does not hold (older than its
// newest 100, a search hit): the message is read once, and my reaction goes.
func TestReactDropOnStoreMiss(t *testing.T) {
	reqs := make(chan string, 8)
	c := testClient(make(chan model.Event, 4))
	useTestAPI(t, c, oldMessage(t, reqs))
	c.React(context.Background(), &model.Chat{ID: 5, Peer: peer{Channel: 5}}, int(msgID(900)), "")
	for {
		select {
		case r := <-reqs:
			if strings.HasPrefix(r, "DELETE ") && strings.HasSuffix(r, "/reactions/🔥/@me") {
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("my reaction was not dropped")
		}
	}
}

// "i" on a message the store does not hold reads the page around it: the
// single-message route answers bots only.
func TestInfoOnStoreMiss(t *testing.T) {
	ev := make(chan model.Event, 4)
	c := testClient(ev)
	useTestAPI(t, c, oldMessage(t, make(chan string, 8)))
	c.Info(context.Background(), &model.Chat{ID: 5, Peer: peer{Channel: 5}}, int(msgID(900)), 0, 0)
	e := next(t, ev).(model.EvInfo)
	if len(e.Lines) == 0 || e.Lines[0] != i18n.T("info_sent_at", i18n.LocalTime(msgID(900).Time())) {
		t.Fatalf("info lines %q, want the date first", e.Lines)
	}
}

func TestFetchRefusesLocalAddressAndRedirect(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "http://127.0.0.1/private", http.StatusFound)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "f")
	if err := fetch(context.Background(), srv.URL, path, 100); err == nil || calls.Load() != 0 {
		t.Fatal("direct local media request was not refused")
	}
	useTestCDN(t, srv.URL)
	if err := fetch(context.Background(), "https://cdn.discordapp.com/test", path, 100); err == nil || calls.Load() != 1 {
		t.Fatalf("redirect escaped the CDN: calls %d, error %v", calls.Load(), err)
	}
}
