package ui

import (
	"slices"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
)

type authPrompt struct {
	net    string
	prompt model.EvAuthPrompt
}

func (u *UI) authStart(net string, prompt model.EvAuthPrompt) {
	if u.prompt != nil && u.promptNet != net {
		u.authPending = slices.DeleteFunc(u.authPending, func(p authPrompt) bool { return p.net == net })
		u.authPending = append(u.authPending, authPrompt{net, prompt})
		return
	}
	if u.authDraft == nil {
		draft := u.ed
		u.authDraft = &draft
		u.authWindow, u.authDebug = u.ws.Current(), u.showDebug
		u.cancelMode()
		u.closeViewer()
		if u.gifs != nil {
			u.gifClose()
		}
		u.picker, u.menu, u.newChat, u.gsearch, u.form, u.themePick, u.hub = nil, nil, nil, nil, nil, nil, nil
		u.pasteAsk = ""
	}
	u.ed = Editor{}
	u.prompt, u.promptNet = &prompt, net
	u.status0(prompt.Question)
	u.goTo(0)
	if qr, ok := u.authQR[net]; ok {
		u.setQRFor(net, qr)
	}
}

func (u *UI) authDone(net string) {
	delete(u.authQR, net)
	u.authPending = slices.DeleteFunc(u.authPending, func(p authPrompt) bool { return p.net == net })
	if u.qr != nil && u.qr.net == net {
		u.closeQR()
	}
	if u.prompt == nil || u.promptNet != net {
		return
	}
	u.prompt, u.promptNet = nil, ""
	u.ed = Editor{}
	if len(u.authPending) > 0 {
		next := u.authPending[0]
		u.authPending = u.authPending[1:]
		u.authStart(next.net, next.prompt)
	} else if u.authDraft != nil {
		if i := slices.Index(u.ws.List, u.authWindow); i >= 0 {
			u.goTo(i)
		}
		u.showDebug = u.authDebug
		u.authWindow = nil
		u.ed = *u.authDraft
		u.authDraft = nil
	}
}

func (u *UI) authKey(k term.Key) {
	switch k.Code {
	case term.Enter:
		u.submit()
	case term.Backspace:
		u.ed.Backspace()
	case term.Delete:
		u.ed.Delete()
	case term.Left:
		u.ed.Left()
	case term.Right:
		u.ed.Right()
	case term.Home:
		u.ed.Home()
	case term.End:
		u.ed.End()
	case term.Ctrl:
		switch k.Rune {
		case 'c':
			u.cancel()
		case 'a':
			u.ed.Home()
		case 'e':
			u.ed.End()
		case 'k':
			u.ed.KillToEnd()
		case 'w':
			u.ed.KillWord()
		}
	case term.Paste:
		u.pasteText(u.view(), normalizePaste(k.Text))
	case term.None:
		if k.Rune != 0 && !k.Alt {
			u.ed.Insert(string(k.Rune))
		}
	}
}

func (u *UI) authShowQR(net string, qr model.EvQR) {
	if u.authQR == nil {
		u.authQR = map[string]model.EvQR{}
	}
	u.authQR[net] = qr
	if u.prompt != nil && u.promptNet == net {
		u.setQRFor(net, qr)
	}
}

func (u *UI) setQRFor(net string, qr model.EvQR) {
	u.setQR(qr)
	if u.qr != nil {
		u.qr.net = net
	}
}
