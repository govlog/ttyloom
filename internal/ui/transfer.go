package ui

import (
	"cmp"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
)

// downloadProgress : a report of a download on its way (model.Progress: at
// most one a whole percent). The label of the line of the file shows the
// percent, on every network; the lines that show md are drawn again only
// when the percent changes. A file that never downloads on its own (a
// document, a DCC offer) was asked for by a key, a menu or a command, and
// its line may be off the screen: the status bar names it too.
func (u *UI) downloadProgress(e model.EvDownloadProgress) {
	md := e.Media
	// Late reports of a parallel download (out of order, or past the
	// landing) never move the count back nor touch a decoding.
	if md.State != model.MediaLoading || u.decodes[md] != nil {
		return
	}
	was, shown := render.LoadPercent(md)
	md.DLDone, md.DLTotal = max(md.DLDone, e.Done), e.Total
	pct, ok := render.LoadPercent(md)
	if !ok || shown && pct == was {
		return
	}
	u.invalidateMedia(md)
	if !md.Previewable() {
		u.flash(i18n.T("downloading_pct", render.CleanLine(cmp.Or(md.Name, md.Label)), pct))
	}
}

// settleUpload : the receipt of a send ends the transfer of its file,
// whatever the result. The placeholder of the pending line (a media with no
// handle of the network) stops loading: a failure says why on a line of its
// own, and the echo of the network, when one comes, brings the real media.
func (u *UI) settleUpload(e model.EvSent) {
	for _, it := range u.eachMsg(u.evKey(e.ChatID)) {
		if md := it.Msg.Media; it.Msg.TmpID == e.TmpID && md != nil && md.Loc == nil && md.State == model.MediaLoading {
			md.State = model.MediaNone
			it.Invalidate()
		}
	}
}
