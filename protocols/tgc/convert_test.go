package tgc

import (
	"context"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/model"
)

func TestMediaOfGIF(t *testing.T) {
	doc := &tg.Document{ID: 1, AccessHash: 2, MimeType: "video/mp4", Size: 1258291,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeAnimated{},
			&tg.DocumentAttributeVideo{Duration: 3.2, W: 320, H: 240},
		}}
	m := mediaOf(&tg.MessageMediaDocument{Document: doc})
	if m.Kind != model.MediaGIF || m.Label != "[gif 0:03 · 1,2 Mo]" || m.Ext != ".mp4" || m.W != 320 || m.Loc == nil {
		t.Fatalf("%+v", m)
	}
}

func TestExtOf(t *testing.T) {
	cases := []struct{ name, mime, want string }{
		{"", "image/jpeg", ".jpg"},
		{"clip.avi", "video/mp4", ".mp4"},
		{"doc.pdf", "application/pdf", ".pdf"},
		{"x", "application/pdf", ".pdf"},
		{"evil.html", "application/octet-stream", ".bin"},
		{"evil.desktop", "application/octet-stream", ".bin"},
		{"archive.tar.gz", "application/octet-stream", ".gz"},
		{"sans-extension", "application/octet-stream", ".bin"},
	}
	for _, c := range cases {
		if got := extOf(c.name, c.mime); got != c.want {
			t.Errorf("extOf(%q, %q) = %q, want %q", c.name, c.mime, got, c.want)
		}
	}
}

func TestMediaOfPhotoPicksSizeUnder800(t *testing.T) {
	p := &tg.Photo{ID: 1, Sizes: []tg.PhotoSizeClass{
		&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 20000},
		&tg.PhotoSize{Type: "x", W: 800, H: 600, Size: 80000},
		&tg.PhotoSize{Type: "y", W: 1280, H: 960, Size: 200000},
	}}
	m := mediaOf(&tg.MessageMediaPhoto{Photo: p})
	if m.Kind != model.MediaPhoto || m.W != 800 || m.Label != "[photo 800x600 · 78 Ko]" {
		t.Fatalf("%+v", m)
	}
	if loc, ok := m.Loc.(*tg.InputPhotoFileLocation); !ok || loc.ThumbSize != "x" {
		t.Fatalf("loc: %+v", m.Loc)
	}
}

func TestMediaOfSticker(t *testing.T) {
	doc := &tg.Document{MimeType: "image/webp", Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeSticker{Alt: "😃"}}}
	m := mediaOf(&tg.MessageMediaDocument{Document: doc})
	if m.Kind != model.MediaSticker || m.Animated || m.Label != "[sticker 😃]" {
		t.Fatalf("%+v", m)
	}
}

func TestFormatStatus(t *testing.T) {
	now := time.Date(2026, 8, 30, 15, 0, 0, 0, time.UTC)
	at := func(y, mo, d, h, mi int) int {
		return int(time.Date(y, time.Month(mo), d, h, mi, 0, 0, time.UTC).Unix())
	}
	cases := []struct {
		s    tg.UserStatusClass
		want string
	}{
		{&tg.UserStatusOnline{Expires: at(2026, 8, 30, 15, 5)}, "en ligne"},
		{&tg.UserStatusOnline{Expires: at(2026, 8, 30, 14, 55)}, "vu récemment"}, // TTL expired
		{&tg.UserStatusOffline{WasOnline: at(2026, 8, 30, 14, 55)}, "vu il y a 5 min"},
		{&tg.UserStatusOffline{WasOnline: at(2026, 8, 30, 12, 0)}, "vu il y a 3 h"},
		{&tg.UserStatusOffline{WasOnline: at(2026, 8, 30, 0, 30)}, "vu il y a 14 h"},
		{&tg.UserStatusOffline{WasOnline: at(2026, 8, 29, 20, 0)}, "vu hier"},
		{&tg.UserStatusOffline{WasOnline: at(2026, 8, 30, 15, 10)}, "vu il y a 1 min"}, // clock running fast
		{&tg.UserStatusOffline{WasOnline: at(2026, 8, 12, 9, 0)}, "vu le 12/08"},
		{&tg.UserStatusRecently{}, "vu récemment"},
		{&tg.UserStatusLastWeek{}, "vu cette semaine"},
		{&tg.UserStatusLastMonth{}, "vu ce mois-ci"},
		{&tg.UserStatusEmpty{}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := formatStatus(c.s, now); got != c.want {
			t.Errorf("formatStatus(%T) = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestWhoisLines(t *testing.T) {
	now := time.Date(2026, 8, 30, 15, 0, 0, 0, time.UTC)
	u := &tg.User{ID: 42, FirstName: "Alice", LastName: "Martin", Username: "alice", Phone: "33612345678",
		Status: &tg.UserStatusRecently{}, Premium: true}
	got := whoisLines(u, tg.UserFull{ID: 42, About: "hacker\ndu dimanche", CommonChatsCount: 2}, now)
	want := []string{"Alice Martin (@alice)", "téléphone : +33612345678", "bio : hacker du dimanche",
		"vu récemment", "2 discussions en commun", "premium"}
	if !slices.Equal(got, want) {
		t.Fatalf("%q", got)
	}
	if got := whoisLines(nil, tg.UserFull{ID: 42}, now); got != nil {
		t.Fatalf("no user: %q", got)
	}
}

// A Client that is offline: api nil, any network call would panic the test.
func TestConvertNamesFromEntities(t *testing.T) {
	c := &Client{peers: peers.Options{}.Build(nil), seen: map[int64]peers.Peer{}}
	chat := &model.Chat{ID: -123, Title: "le groupe"}
	ent := peer.NewEntities(map[int64]*tg.User{42: {ID: 42, FirstName: "Alice", LastName: "Martin"}}, nil, nil)
	msg := func(from int64) *tg.Message {
		return &tg.Message{ID: 7, PeerID: &tg.PeerChat{ChatID: 123}, FromID: &tg.PeerUser{UserID: from},
			Date: 1, Message: "salut"}
	}

	m, _, ok := c.convert(context.Background(), msg(42), ent, chat)
	if !ok || m.From != "Alice Martin" || m.FromID != 42 {
		t.Fatalf("from entities: %v %+v", ok, m)
	}
	m, _, ok = c.convert(context.Background(), msg(99), ent, chat)
	if !ok || m.From != "?" {
		t.Fatalf("unknown: %v %+v", ok, m)
	}
	// 42 has been seen: it comes back with no entity and no network.
	m, _, ok = c.convert(context.Background(), msg(42), peer.Entities{}, chat)
	if !ok || m.From != "Alice Martin" {
		t.Fatalf("from local cache: %v %+v", ok, m)
	}
}

// Private chat: from_id missing on the MTProto side for incoming as well as
// outgoing messages. The avatar must fall back to the peer (in) or to me (out).
func TestAvatarPrivate(t *testing.T) {
	c := &Client{peers: peers.Options{}.Build(nil), seen: map[int64]peers.Peer{}, me: &tg.User{ID: 7}}
	loc := &tg.InputFileLocation{}
	chat := &model.Chat{ID: 42, Kind: model.ChatUser, Title: "Alice", PhotoLoc: loc}

	in := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 42}, Date: 1, Message: "salut"}
	m, _, ok := c.convert(context.Background(), in, peer.Entities{}, chat)
	if !ok || m.FromID != chat.ID || m.FromPhoto != loc {
		t.Fatalf("incoming private: %v %+v", ok, m)
	}

	out := &tg.Message{ID: 2, PeerID: &tg.PeerUser{UserID: 42}, Date: 2, Message: "salut", Out: true}
	m, _, ok = c.convert(context.Background(), out, peer.Entities{}, chat)
	if !ok || m.FromID != c.me.ID {
		t.Fatalf("outgoing private: %v %+v", ok, m)
	}
}

func TestMediaOfWebPage(t *testing.T) {
	desc := strings.Repeat("ab", 150) // 300 characters: cut to 200 + …
	p := &tg.WebPage{ID: 1, URL: "https://exemple.fr/article", SiteName: "Exemple",
		Title: "Un titre", Description: desc,
		Photo: &tg.Photo{ID: 2, Sizes: []tg.PhotoSizeClass{
			&tg.PhotoSize{Type: "s", W: 320, H: 240, Size: 12000},
			&tg.PhotoSize{Type: "x", W: 800, H: 600, Size: 90000}, // above the thumbnail cap
		}}}
	m := mediaOf(&tg.MessageMediaWebPage{Webpage: p})
	if m.Kind != model.MediaWebPage || m.Label != "[lien · Exemple · Un titre]" || m.URL != p.URL {
		t.Fatalf("%+v", m)
	}
	if m.W != 320 || m.Size != 12000 || !m.Previewable() {
		t.Fatalf("thumbnail: %+v", m)
	}
	if loc, ok := m.Loc.(*tg.InputPhotoFileLocation); !ok || loc.ThumbSize != "s" {
		t.Fatalf("loc: %+v", m.Loc)
	}
	if len([]rune(m.Name)) != 200 || !strings.HasSuffix(m.Name, "…") { // "…" included
		t.Fatalf("description: %d %q", len([]rune(m.Name)), m.Name)
	}

	// With no photo: label cut to the parts present, nothing to preview.
	m = mediaOf(&tg.MessageMediaWebPage{Webpage: &tg.WebPage{URL: "https://exemple.fr", Title: "Seul"}})
	if m.Label != "[lien · Seul]" || m.Loc != nil || m.Previewable() || m.Name != "" {
		t.Fatalf("no photo: %+v", m)
	}
	// Preview not resolved by Telegram yet: no media.
	if m := mediaOf(&tg.MessageMediaWebPage{Webpage: &tg.WebPagePending{}}); m != nil {
		t.Fatalf("pending : %+v", m)
	}
}

func TestMediaOfGeo(t *testing.T) {
	geo := &tg.GeoPoint{Lat: 48.8566, Long: 2.3522}
	wantURL := "https://www.openstreetmap.org/?mlat=48.85660&mlon=2.35220#map=15/48.85660/2.35220"
	m := mediaOf(&tg.MessageMediaGeo{Geo: geo})
	if m.Kind != model.MediaMap || m.Lat != 48.8566 || m.Long != 2.3522 || m.Label != "[localisation 48.85660,2.35220]" || m.URL != wantURL {
		t.Fatalf("geo : %+v", m)
	}
	if !m.Previewable() || m.Loc != nil {
		t.Fatalf("geo previewable/loc : %+v", m)
	}

	m = mediaOf(&tg.MessageMediaGeoLive{Geo: geo})
	if m.Kind != model.MediaMap || m.Label != "[position en direct 48.85660,2.35220]" || m.URL != wantURL {
		t.Fatalf("geo live : %+v", m)
	}

	m = mediaOf(&tg.MessageMediaVenue{Geo: geo, Title: "Tour Eiffel", Address: "Champ de Mars"})
	if m.Kind != model.MediaMap || m.Lat != 48.8566 || m.Long != 2.3522 || m.Label != "[lieu · Tour Eiffel · Champ de Mars]" || m.URL != wantURL {
		t.Fatalf("venue : %+v", m)
	}

	// Wild position (NaN): refused, no media — no bad tile and no huge file name
	// downstream.
	if m := mediaOf(&tg.MessageMediaGeo{Geo: &tg.GeoPoint{Lat: math.NaN(), Long: 2.35}}); m != nil {
		t.Fatalf("NaN : %+v", m)
	}

	// Out of bounds but finite (wild position of a sloppy client): clamped
	// rather than refused, into the valid Web Mercator projection.
	m = mediaOf(&tg.MessageMediaGeo{Geo: &tg.GeoPoint{Lat: 91, Long: 200}})
	if m.Lat != 85.0511 || m.Long != 180 {
		t.Fatalf("clamp : %+v", m)
	}
}

// TestExtAllowList : with an unknown mime type, only an extension of the
// allow list survives; everything that runs something under xdg-open is saved
// as .bin.
func TestExtAllowList(t *testing.T) {
	for _, name := range []string{
		"facture.jar", "start.jnlp", "pkg.deb", "pkg.rpm", "s.py", "s.pl", "s.rb",
		"x.php", "x.sh", "x.desktop", "x.html", "x.svg", "x.js", "x.exe", "x.msi",
		"x.apk", "x.dmg", "x.ps1", "x.bat", "x.cmd", "x.lnk", "x.scr", "x.vbs",
		"x.wsf", "x.reg", "x.hta", "x.run", "sans-extension",
	} {
		if got := extOf(name, "application/octet-stream"); got != ".bin" {
			t.Errorf("extOf(%q) = %q, want .bin", name, got)
		}
	}
	for _, c := range []struct{ name, want string }{
		{"photo.JPG", ".jpg"}, {"clip.mp4", ".mp4"}, {"note.txt", ".txt"},
		{"tableur.xlsx", ".xlsx"}, {"archive.zip", ".zip"}, {"dump.tar", ".tar"},
		{"notes.md", ".md"}, {"data.json", ".json"}, {"livre.pdf", ".pdf"},
	} {
		if got := extOf(c.name, "application/octet-stream"); got != c.want {
			t.Errorf("extOf(%q) = %q, want %q", c.name, got, c.want)
		}
	}
	// The mime type still wins over the name.
	if got := extOf("facture.jar", "image/png"); got != ".png" {
		t.Errorf("mime priority: %q", got)
	}
}
