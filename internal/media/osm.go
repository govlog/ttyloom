// osm.go : static OpenStreetMap map for a location (MediaMap).

package media

import (
	"bytes"
	"context"
	"fmt"
	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const MapCopyrightURL = "https://www.openstreetmap.org/copyright"
const MapAttribution = "© OpenStreetMap contributors · " + MapCopyrightURL

const (
	mapZoom       = 15                                                 // fixed zoom level of the map
	tileSize      = 256                                                // px per OSM tile
	mapTimeout    = 30 * time.Second                                   // network budget for the whole map (4 tiles), not per tile
	maxTileBytes  = 2 << 20                                            // 2 MB: a PNG tile rarely goes over 50 KB
	maxTilePixels = 1 << 22                                            // decompression guard, even after the 256x256 check
	userAgent     = "ttyloom/1.0 (+https://github.com/govlog/ttyloom)" // OSM policy: UA + contact are required
	maxLat        = 85.0511                                            // bounds of the Web Mercator projection
	minLat        = -85.0511
)

// the time budget comes from the context (mapTimeout in Tile), not from a fixed timeout per request.
var tileClient = &http.Client{}

// clampF/clampInt : plain bounding, used by tileCoord and Tile.
func clampF(v, lo, hi float64) float64 { return max(lo, min(hi, v)) }
func clampInt(v, lo, hi int) int       { return max(lo, min(hi, v)) }

// clampCoord gives safe coordinates for a tile computation — NaN/Inf brought
// back to 0 (no real position gives them), latitude bounded to the Web
// Mercator projection, longitude to ±180°.
func clampCoord(lat, lon float64) (float64, float64) {
	if math.IsNaN(lat) || math.IsInf(lat, 0) {
		lat = 0
	}
	if math.IsNaN(lon) || math.IsInf(lon, 0) {
		lon = 0
	}
	return clampF(lat, minLat, maxLat), clampF(lon, -180, 180)
}

// tileCoord : floating position in the tile grid at zoom z, from coordinates
// already bounded. Tile also needs the fraction inside the middle tile, for
// the marker.
func tileCoord(lat, lon float64, z int) (xf, yf float64) {
	lat, lon = clampCoord(lat, lon)
	n := math.Exp2(float64(z))
	xf = (lon + 180.0) / 360.0 * n
	latRad := lat * math.Pi / 180.0
	yf = (1.0 - math.Log(math.Tan(latRad)+1.0/math.Cos(latRad))/math.Pi) / 2.0 * n
	return xf, yf
}

// MapName gives the cache file name for lat/lon/zoom — never a remote string
// (the Title/Address of a venue, say), always these formatted floats,
// bounded (a wild value does not give a name of 600 characters).
func MapName(lat, lon float64) string {
	lat, lon = clampCoord(lat, lon)
	return fmt.Sprintf("%.5f_%.5f_%d_attributed.png", lat, lon, mapZoom)
}

// fetchTile gets one raw OSM tile (z/x/y), User-Agent required, size bounded,
// dimensions checked before the full decoding (256x256 expected — everything
// else is refused, plus the decompression guard).
func fetchTile(ctx context.Context, z, x, y int) (image.Image, error) {
	// Cache by tile, so nearby locations reuse the same downloads. No automatic
	// expiry: the tile policy requires at least seven days without HTTP caching.
	path := filepath.Join(config.CacheDir(), "maptiles", fmt.Sprintf("%d_%d_%d.png", z, x, y))
	if f, err := os.Open(path); err == nil {
		body, readErr := io.ReadAll(io.LimitReader(f, maxTileBytes+1))
		f.Close()
		if readErr == nil {
			if img, err := decodeTile(body, z, x, y); err == nil {
				return img, nil
			}
		}
	}
	url := fmt.Sprintf("https://tile.openstreetmap.org/%d/%d/%d.png", z, x, y)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := tileClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(i18n.T("osm_tile_error"), z, x, y, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTileBytes+1))
	if err != nil {
		return nil, err
	}
	img, err := decodeTile(body, z, x, y)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := config.WriteAtomic(path, body, 0o600); err != nil {
		return nil, err
	}
	return img, nil
}

func decodeTile(body []byte, z, x, y int) (image.Image, error) {
	if len(body) > maxTileBytes {
		return nil, fmt.Errorf(i18n.T("osm_tile_too_big"), z, x, y)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if cfg.Width != tileSize || cfg.Height != tileSize || cfg.Width*cfg.Height > maxTilePixels {
		return nil, fmt.Errorf(i18n.T("osm_tile_bad_size"), z, x, y, cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	return img, err
}

// Tile builds a 512x512 image (2x2 OSM tiles) centred on lat/lon, with a red
// marker at the point, and writes it to path. File already there: no network
// (OSM policy — no useless refresh). NaN/Inf: refused before any disk or
// network access.
func Tile(ctx context.Context, lat, lon float64, path string) error {
	if math.IsNaN(lat) || math.IsInf(lat, 0) || math.IsNaN(lon) || math.IsInf(lon, 0) {
		return fmt.Errorf(i18n.T("bad_coordinates"), lat, lon)
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, mapTimeout) // the whole map (4 tiles), not tile by tile
	defer cancel()

	xf, yf := tileCoord(lat, lon, mapZoom)
	top := int(math.Exp2(float64(mapZoom))) - 1
	x, y := clampInt(int(xf), 0, top), clampInt(int(yf), 0, top)
	px, py := int((xf-float64(int(xf)))*tileSize), int((yf-float64(int(yf)))*tileSize)

	// x0,y0 : top left tile of the 2x2 block — the one of the two neighbours that
	// leaves the point in the inner half of the block rather than on an edge.
	// ponytail: tiles on the fixed grid (256 px); the point stays within ±128 px
	// of the exact centre (256,256), not cropped to the pixel — a sub-tile crop
	// would centre exactly, which a message thumbnail does not need.
	x0, y0 := x-1, y-1
	if px >= tileSize/2 {
		x0 = x
	}
	if py >= tileSize/2 {
		y0 = y
	}
	// the 2x2 block always stays in the grid, even for a point on the edge (x0+1 ≤ top).
	x0, y0 = clampInt(x0, 0, top-1), clampInt(y0, 0, top-1)
	canvas := image.NewRGBA(image.Rect(0, 0, 2*tileSize, 2*tileSize))
	for dy := 0; dy < 2; dy++ {
		for dx := 0; dx < 2; dx++ {
			t, err := fetchTile(ctx, mapZoom, x0+dx, y0+dy)
			if err != nil {
				return err
			}
			r := image.Rect(dx*tileSize, dy*tileSize, (dx+1)*tileSize, (dy+1)*tileSize)
			draw.Draw(canvas, r, t, t.Bounds().Min, draw.Src)
		}
	}
	drawMarker(canvas, (x-x0)*tileSize+px, (y-y0)*tileSize+py)
	drawMapCredit(canvas)
	return writeAtomicPNG(path, canvas)
}

// Keep attribution in the saved PNG as well as the terminal text.
func drawMapCredit(img *image.RGBA) {
	bottom := img.Bounds().Max.Y
	draw.Draw(img, image.Rect(0, bottom-32, img.Bounds().Max.X, bottom), image.White, image.Point{}, draw.Src)
	d := font.Drawer{Dst: img, Src: image.Black, Face: basicfont.Face7x13}
	d.Dot = fixed.P(6, bottom-19)
	d.DrawString("(c) OpenStreetMap contributors")
	d.Dot = fixed.P(6, bottom-5)
	d.DrawString(MapCopyrightURL)
}

// drawMarker draws a full red marker, 5 px radius, centred on cx,cy.
func drawMarker(img *image.RGBA, cx, cy int) {
	red := color.RGBA{220, 30, 30, 255}
	const rad = 5
	for dy := -rad; dy <= rad; dy++ {
		for dx := -rad; dx <= rad; dx++ {
			if dx*dx+dy*dy <= rad*rad {
				img.Set(cx+dx, cy+dy, red)
			}
		}
	}
}

// writeAtomicPNG writes through config.WriteAtomic (temporary file in the
// final directory, never named after remote data, then rename); the map
// directory and the file stay private (0700/0600).
func writeAtomicPNG(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := encode(img) // PNG encoder shared with Load (media.go)
	if err != nil {
		return err
	}
	return config.WriteAtomic(path, b, 0o600)
}
