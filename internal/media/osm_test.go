package media

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"math"
	"net/http"
	"testing"
)

// tileXY : tile indexes of lat/lon at zoom z, the integer part of tileCoord
// bounded to the grid — what Tile does before picking its 2x2 block.
func tileXY(lat, lon float64, z int) (x, y int) {
	xf, yf := tileCoord(lat, lon, z)
	top := int(math.Exp2(float64(z))) - 1
	return clampInt(int(xf), 0, top), clampInt(int(yf), 0, top)
}

// TestTileXY : Paris, zoom 15 — fixed values from the Web Mercator formula
// (computed apart, see osm.go).
func TestTileXY(t *testing.T) {
	x, y := tileXY(48.8566, 2.3522, 15)
	if x != 16598 || y != 11273 {
		t.Fatalf("Paris z15: x=%d y=%d, want 16598,11273", x, y)
	}
}

// TestMapName : cache name built from the formatted floats — never from
// remote text (the Title/Address of a venue, say).
func TestMapName(t *testing.T) {
	if got := MapName(48.8566, 2.3522); got != "48.85660_2.35220_15_attributed.png" {
		t.Fatalf("%q", got)
	}
	if got := MapName(-33.86785, 151.20732); got != "-33.86785_151.20732_15_attributed.png" {
		t.Fatalf("negative: %q", got)
	}
}

// TestTileXYClamp : coordinates out of bounds or not finite — always a tile
// of the grid on the output (P1: otherwise 4 requests answered 400 by OSM and
// a file name of several hundred characters).
func TestTileXYClamp(t *testing.T) {
	const top = 1<<15 - 1 // zoom 15: 32768x32768 grid
	valid := func(x, y int) bool { return x >= 0 && x <= top && y >= 0 && y <= top }
	if x, y := tileXY(90, 180, 15); !valid(x, y) {
		t.Fatalf("pole/antimeridian: x=%d y=%d", x, y)
	}
	if x, y := tileXY(-91, -200, 15); !valid(x, y) {
		t.Fatalf("out of bounds negative: x=%d y=%d", x, y)
	}
	if x, y := tileXY(math.NaN(), math.Inf(1), 15); !valid(x, y) {
		t.Fatalf("NaN/Inf : x=%d y=%d", x, y)
	}
}

type tileTransport func(*http.Request) (*http.Response, error)

func (f tileTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTileReusesDownloadedTile(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir())
	var body bytes.Buffer
	if err := png.Encode(&body, image.NewRGBA(image.Rect(0, 0, tileSize, tileSize))); err != nil {
		t.Fatal(err)
	}
	calls := 0
	before := tileClient
	t.Cleanup(func() { tileClient = before })
	tileClient = &http.Client{Transport: tileTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("User-Agent") != userAgent {
			t.Error("missing application User-Agent")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body.Bytes()))}, nil
	})}
	for range 2 {
		if _, err := fetchTile(context.Background(), 15, 16598, 11273); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("same tile fetched %d times", calls)
	}
}
