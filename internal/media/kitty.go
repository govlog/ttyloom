package media

import (
	"encoding/base64"
	"fmt"
	"image"
	"strings"
)

const chunk = 4096

// KittyDisplay sends and places the image on cols x rows cells at the cursor
// (a=T), under the placement id pid. The pair (image, pid) names the
// placement: the same pair replaces in place, two different pids live
// together (a repeated avatar). The caller keeps stable pids from one frame
// to the next. crop, when given and not empty, limits the display to that
// sub-rectangle of the image sent (zoomed preview).
func KittyDisplay(id, pid uint32, png []byte, cols, rows int, crop ...image.Rectangle) string {
	return kittyChunks(fmt.Sprintf("a=T,f=100,i=%d,p=%d,c=%d,r=%d%s,q=2",
		id, pid, cols, rows, cropKeys(crop)), png)
}

// KittyPlace places an image already sent at the cursor, under the id pid.
// KittyTransmit sends the bytes of the image under id and shows nothing: the
// cursor is not involved, KittyPlace puts it on the screen afterwards.
func KittyTransmit(id uint32, png []byte) string {
	return kittyChunks(fmt.Sprintf("a=t,f=100,i=%d,q=2", id), png)
}

func KittyPlace(id, pid uint32, cols, rows int, crop ...image.Rectangle) string {
	return fmt.Sprintf("\x1b_Ga=p,i=%d,p=%d,c=%d,r=%d%s,q=2\x1b\\",
		id, pid, cols, rows, cropKeys(crop))
}

// cropKeys : x,y,w,h of the kitty protocol — left edge, top edge, width and
// height of the SOURCE rectangle to show, in pixels of the image sent. The
// rectangle is then stretched over the cols x rows cells of the placement,
// which gives a move or a zoom without sending the image again.
func cropKeys(crop []image.Rectangle) string {
	if len(crop) == 0 || crop[0].Empty() {
		return ""
	}
	c := crop[0]
	return fmt.Sprintf(",x=%d,y=%d,w=%d,h=%d", c.Min.X, c.Min.Y, c.Dx(), c.Dy())
}

// KittyDeletePlacement drops placements without touching the data sent
// (lower case d=i): the one of pid, or every one of the image when pid is 0.
func KittyDeletePlacement(id, pid uint32) string {
	if pid == 0 {
		return fmt.Sprintf("\x1b_Ga=d,d=i,i=%d,q=2\x1b\\", id)
	}
	return fmt.Sprintf("\x1b_Ga=d,d=i,i=%d,p=%d,q=2\x1b\\", id, pid)
}

// KittyFree drops the image and frees its data.
func KittyFree(id uint32) string { return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id) }

// kittyChunks gives data in base64, chunk characters per sequence, ctrl on
// the first. Animation sends every frame again at each tick: the encoding
// goes chunk by chunk into one allocation of the final size. 3072 bytes give
// exactly 4096 characters, so no padding comes before the last chunk.
func kittyChunks(ctrl string, data []byte) string {
	const raw = chunk / 4 * 3
	n := (len(data) + raw - 1) / raw
	var b strings.Builder
	b.Grow(len(ctrl) + base64.StdEncoding.EncodedLen(len(data)) + 10*n)
	var enc [chunk]byte
	for i := 0; i < len(data); i += raw {
		part := data[i:min(i+raw, len(data))]
		if i == 0 {
			b.WriteString("\x1b_G" + ctrl + ",m=")
		} else {
			b.WriteString("\x1b_Gm=")
		}
		if i+raw < len(data) {
			b.WriteString("1;")
		} else {
			b.WriteString("0;")
		}
		m := base64.StdEncoding.EncodedLen(len(part))
		base64.StdEncoding.Encode(enc[:m], part)
		b.Write(enc[:m])
		b.WriteString("\x1b\\")
	}
	return b.String()
}
