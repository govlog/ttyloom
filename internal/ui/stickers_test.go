package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
)

func (f *fakeBackend) LoadStickers(context.Context, *model.Chat) {
	f.stickerCalls = append(f.stickerCalls, "load")
}
func (f *fakeBackend) LoadStickerPack(_ context.Context, _ *model.Chat, key string) {
	f.stickerCalls = append(f.stickerCalls, "pack "+key)
}
func (f *fakeBackend) SearchStickers(_ context.Context, _ *model.Chat, e string) {
	f.stickerCalls = append(f.stickerCalls, "search "+e)
}
func (f *fakeBackend) SendSticker(_ context.Context, _ *model.Chat, s model.Sticker, _ int64) {
	f.stickersSent = append(f.stickersSent, s)
}

// The search of the Stickers tab asks Telegram for the stickers of one emoji:
// an emoji typed as it is, else the first one whose name matches; none for a
// word no emoji has.
func TestStickerQuery(t *testing.T) {
	for q, want := range map[string]string{"😂": "😂", "joy": "😂", "zzzqqq": ""} {
		got, ok := stickerQuery(q)
		if got != want || ok != (want != "") {
			t.Errorf("stickerQuery(%q) = %q, %v; want %q", q, got, ok, want)
		}
	}
}

func sticker(name string) model.Sticker {
	return model.Sticker{Name: name, Emoji: "😀", Send: name,
		Preview: &model.Media{Kind: model.MediaSticker, Name: name, Loc: name}}
}

// stickerUI : the GIF box open on a Telegram chat, then its Stickers tab.
func stickerUI(t *testing.T) (*UI, *fakeBackend) {
	t.Helper()
	u, b := gifUI()
	u.ws.Cur = 1
	u.openGifs("")
	u.key(term.Key{Code: term.Tab})
	if u.stickers == nil {
		t.Fatal("Tab in the GIF box: no Stickers tab")
	}
	return u, b
}

// cellNames : the cells of the box, a title as "# title".
func cellNames(s *stickerBox) []string {
	var out []string
	for _, c := range s.cells {
		if c.s == nil {
			out = append(out, "# "+c.title)
		} else {
			out = append(out, c.s.Name)
		}
	}
	return out
}

// Tab goes from the GIF box to its Stickers tab and back, on the same chat;
// the stickers are asked for once.
func TestStickerTabSwitch(t *testing.T) {
	u, b := stickerUI(t)
	if u.gifs != nil || !slices.Equal(b.stickerCalls, []string{"load"}) {
		t.Fatalf("Stickers tab: gifs %v, calls %v; want the GIF box closed and one load", u.gifs != nil, b.stickerCalls)
	}
	u.key(term.Key{Code: term.Tab})
	if u.gifs == nil || u.stickers != nil {
		t.Fatal("Tab again: the GIF tab is not back")
	}
}

// The sections show as a title cell then their stickers; a pack not loaded has
// its title alone, and the next one is asked for once the end of the list is
// on the screen — one at a time.
func TestStickerSectionsAndLazyPack(t *testing.T) {
	u, b := stickerUI(t)
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvStickers{ChatID: 1, Sections: []model.StickerSection{
		{Title: "Recent", Stickers: []model.Sticker{sticker("a"), sticker("b")}},
		{Title: "P1", Key: "p1"}, {Title: "P2", Key: "p2"}}}})
	if got := cellNames(u.stickers); !slices.Equal(got, []string{"# Recent", "a", "b", "# P1", "# P2"}) {
		t.Fatalf("cells: %v", got)
	}
	u.key(term.Key{Code: term.Right}) // a move while the pack is on its way asks nothing more
	if !slices.Equal(b.stickerCalls, []string{"load", "pack p1"}) {
		t.Fatalf("calls: %v, want the first pack only", b.stickerCalls)
	}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvStickerPack{ChatID: 1, Key: "p1", Stickers: []model.Sticker{sticker("c")}}})
	if got := cellNames(u.stickers); !slices.Equal(got, []string{"# Recent", "a", "b", "# P1", "c", "# P2"}) {
		t.Fatalf("cells after the pack: %v", got)
	}
	if !slices.Equal(b.stickerCalls, []string{"load", "pack p1", "pack p2"}) {
		t.Fatalf("calls: %v, want the next pack", b.stickerCalls)
	}
}

// Enter on a title sends nothing; on a sticker it sends it, closes the box and
// leaves a pending message in the window.
func TestStickerSendSkipsTitles(t *testing.T) {
	u, b := stickerUI(t)
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvStickers{ChatID: 1, Sections: []model.StickerSection{
		{Title: "Recent", Stickers: []model.Sticker{sticker("a")}}}}})
	u.key(term.Key{Code: term.Enter}) // on the title
	if len(b.stickersSent) != 0 || u.stickers == nil {
		t.Fatalf("Enter on a title: sent %v, box open %v", b.stickersSent, u.stickers != nil)
	}
	u.key(term.Key{Code: term.Right})
	u.key(term.Key{Code: term.Enter})
	if len(b.stickersSent) != 1 || b.stickersSent[0].Name != "a" || u.stickers != nil {
		t.Fatalf("Enter on a sticker: sent %v, box open %v", b.stickersSent, u.stickers != nil)
	}
	if w := u.ws.List[1]; len(w.Items) == 0 || w.Items[len(w.Items)-1].Msg == nil {
		t.Fatal("no pending message in the window")
	}
}

// An answer that comes after the box closed is dropped.
func TestStickerLateAnswerIgnored(t *testing.T) {
	u, _ := stickerUI(t)
	u.key(term.Key{Code: term.Esc})
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvStickerPack{ChatID: 1, Key: "p1", Stickers: []model.Sticker{sticker("c")}}})
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvStickers{ChatID: 1}})
	if u.stickers != nil {
		t.Fatal("a late answer opened the box again")
	}
}

// A network with no stickers opens no tab: /sticker says so.
func TestStickerUnsupported(t *testing.T) {
	u, b := gifUI()
	u.ws.Cur = 1
	b.caps = model.Caps{Gifs: true}
	u.openStickers("")
	if u.stickers != nil || len(b.stickerCalls) != 0 {
		t.Fatalf("no sticker capability: box open %v, calls %v", u.stickers != nil, b.stickerCalls)
	}
}

// Telegram: a word no emoji has asks nothing and says there is no result; a
// word that names one asks the network for it once the typing rests.
func TestStickerSearchTelegram(t *testing.T) {
	u, b := stickerUI(t)
	for _, r := range "zzzqqq" {
		u.key(term.Key{Rune: r})
	}
	u.tickAt(time.Now().Add(time.Second))
	if slices.ContainsFunc(b.stickerCalls, func(c string) bool { return len(c) > 6 && c[:6] == "search" }) {
		t.Fatalf("calls %v: a word with no emoji went out", b.stickerCalls)
	}
	for range 6 {
		u.key(term.Key{Code: term.Backspace})
	}
	for _, r := range "joy" {
		u.key(term.Key{Rune: r})
	}
	u.tickAt(time.Now().Add(time.Second))
	if !slices.Contains(b.stickerCalls, "search 😂") {
		t.Fatalf("calls %v: want search 😂", b.stickerCalls)
	}
}

// Discord: the search filters what the box holds, by name, no request.
func TestStickerSearchLocal(t *testing.T) {
	u, b := stickerUI(t)
	b.caps.StickerSearch = false
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvStickers{ChatID: 1, Sections: []model.StickerSection{
		{Title: "Server", Stickers: []model.Sticker{sticker("wave"), sticker("dance"), sticker("Waving cat")}}}}})
	for _, r := range "wav" {
		u.key(term.Key{Rune: r})
	}
	if got := cellNames(u.stickers); !slices.Equal(got, []string{"# Server", "wave", "Waving cat"}) {
		t.Fatalf("filtered cells: %v", got)
	}
	if slices.ContainsFunc(b.stickerCalls, func(c string) bool { return c != "load" }) {
		t.Fatalf("calls %v: a local filter asks nothing", b.stickerCalls)
	}
}

// The text of the network (pack titles, names, errors) is cleaned before it is
// drawn: an escape sequence in a pack title never reaches the terminal.
func TestStickerTextCleaned(t *testing.T) {
	u, _ := stickerUI(t)
	st := sticker("x\x1b[2Jy")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvStickers{ChatID: 1, Err: "e\x1b]0;t\x07",
		Sections: []model.StickerSection{{Title: "a\x1b]0;pwn\x07b", Stickers: []model.Sticker{st}}}}})
	s := u.stickers
	for _, text := range []string{s.cells[0].title, s.cells[1].s.Name, s.err} {
		if strings.ContainsAny(text, "\x1b\x07") {
			t.Fatalf("network text drawn raw: %q", text)
		}
	}
}

// pack(name, key, n) : a loaded section of n stickers.
func pack(title string, n int) model.StickerSection {
	sec := model.StickerSection{Title: title}
	for i := range n {
		sec.Stickers = append(sec.Stickers, sticker(fmt.Sprint(title, i)))
	}
	return sec
}

// A pack is asked for as soon as its title is on the screen, not only when the
// end of the whole list is.
func TestStickerPackLoadsWhenTitleShows(t *testing.T) {
	u, b := stickerUI(t)
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvStickers{ChatID: 1, Sections: []model.StickerSection{
		{Title: "P1", Key: "p1"}, pack("Recent", 200)}}})
	if !slices.Contains(b.stickerCalls, "pack p1") {
		t.Fatalf("calls %v: the title of P1 is on the screen, its pack is not asked", b.stickerCalls)
	}
}

// Telegram: the same search typed again shows its results again; the loaded
// sections show under them, cut to the stickers of that emoji.
func TestStickerSearchAgain(t *testing.T) {
	u, _ := stickerUI(t)
	laugh, smile := sticker("laugh"), sticker("smile")
	laugh.Emoji = "😂"
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvStickers{ChatID: 1, Sections: []model.StickerSection{
		{Title: "Recent", Stickers: []model.Sticker{laugh, smile}}}}})
	typeIn := func(s string) {
		for _, r := range s {
			u.key(term.Key{Rune: r})
		}
		u.tickAt(time.Now().Add(time.Second))
	}
	typeIn("joy")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvStickerSearch{ChatID: 1, Query: "😂", Stickers: []model.Sticker{sticker("r")}}})
	want := []string{"# Résultats", "r", "# Recent", "laugh"}
	if got := cellNames(u.stickers); !slices.Equal(got, want) {
		t.Fatalf("search: %v, want %v", got, want)
	}
	for range 3 {
		u.key(term.Key{Code: term.Backspace})
	}
	u.tickAt(time.Now().Add(time.Second))
	typeIn("joy")
	if got := cellNames(u.stickers); !slices.Equal(got, want) {
		t.Fatalf("same search again: %v, want %v", got, want)
	}
}

// Telegram: a new search answer replaces the results, and the previews of the
// old ones are freed as the GIF box frees its list. A preview of the old
// results whose download lands late decodes nothing.
func TestStickerSearchFreesOldResults(t *testing.T) {
	u, b := stickerUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // a decoding of the test file ends with the test
	u.ctx, u.events = ctx, make(chan model.Event, 4)
	search := func(word, name string) {
		for range len(u.stickers.query) {
			u.key(term.Key{Code: term.Backspace})
		}
		for _, r := range word {
			u.key(term.Key{Rune: r})
		}
		u.tickAt(time.Now().Add(time.Second))
		e, _ := stickerQuery(word)
		u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvStickerSearch{ChatID: 1, Query: e, Stickers: []model.Sticker{sticker(name)}}})
		u.stickerLines(u.stickerRect()) // a frame: the previews on the screen download
	}
	search("joy", "old")
	old := u.stickers.results[0].Preview
	if old.State != model.MediaLoading || len(b.downloads) != 1 {
		t.Fatalf("setup: preview of the first result %v, downloads %v; want it on its way", old.State, b.downloads)
	}
	search("smile", "new")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvDownloaded{Media: old, Path: "/nonexistent.webp"}})
	if old.State != model.MediaNone || old.Path != "" || u.decodes[old] != nil {
		t.Fatalf("late preview of an old result: state %v, path %q, decoding %v; want it dropped",
			old.State, old.Path, u.decodes[old] != nil)
	}
}
