package tgc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// Run blocks: connection, authentication, then the update loop.
func (c *Client) Run(ctx context.Context) error {
	return c.client.Run(ctx, func(ctx context.Context) error {
		if c.cfg.BotToken == "" {
			st, err := c.client.Auth().Status(ctx)
			if err != nil {
				return fmt.Errorf(i18n.T("auth_error"), err)
			}
			if !st.Authorized {
				phone, err := c.qrLogin(ctx)
				if err != nil {
					return err
				}
				// QR taken: IfNecessary sees the authorisation and asks for nothing.
				flow := auth.NewFlow(authenticator{c: c, phone: phone}, auth.SendCodeOptions{})
				if err := c.client.Auth().IfNecessary(ctx, flow); err != nil {
					return fmt.Errorf(i18n.T("auth_error"), err)
				}
			}
		} else {
			st, err := c.client.Auth().Status(ctx)
			if err != nil {
				return fmt.Errorf(i18n.T("auth_bot_error"), err)
			}
			if !st.Authorized {
				if _, err := c.client.Auth().Bot(ctx, c.cfg.BotToken); err != nil {
					return fmt.Errorf(i18n.T("auth_bot_error"), err)
				}
			}
		}
		me, err := c.client.Self(ctx)
		if err != nil {
			return err
		}
		c.me.Store(me)
		return c.gaps.Run(ctx, c.api, me.ID, updates.AuthOptions{IsBot: me.Bot, OnStart: func(ctx context.Context) {
			c.Post(model.EvReady{SelfID: me.ID, SelfName: nick(c.peers.User(me)), Bot: me.Bot})
			go c.loadReactions(ctx)
		}})
	})
}

// --- authentication ---

// Logout ends the account session on the server (auth.logOut): the next Run
// goes through the QR flow again. The session file stays — its auth key is
// still good for a new login.
func (c *Client) Logout(ctx context.Context) error {
	_, err := c.api.AuthLogOut(ctx)
	return err
}

// qrLogin offers the QR login before the phone flow: the QR goes to the UI
// while the prompt waits for Enter, and the first of the two ways that works
// closes the other. It gives back the text already typed (a phone number,
// maybe) when the user prefers the phone, "" when the QR worked.
func (c *Client) qrLogin(ctx context.Context) (string, error) {
	qctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		err := errors.New(i18n.T("qr_interrupted")) // kept if Auth panics
		defer func() { done <- err }()
		defer c.Guard("qrLogin", nil)
		// show is called again at each token renewal: the UI replaces the overlay.
		// The URL is worth an open session, so it is never logged.
		_, err = c.client.QR().Auth(qctx, c.loggedIn, func(_ context.Context, t qrlogin.Token) error {
			c.Post(model.EvQR{URL: t.URL(), Expires: t.Expires(), Hint: "qr_hint_telegram", Keys: "qr_keys_telegram"})
			return nil
		})
	}()
	reply := make(chan string, 1)
	c.Post(model.EvAuthPrompt{Question: i18n.T("qr_prompt"), Reply: reply})
	select {
	case line := <-reply:
		line = strings.TrimSpace(line)
		cancel()
		<-done // no EvQR after this point
		c.Post(model.EvQRDone{})
		return line, nil
	case err := <-done:
		c.Post(model.EvQRDone{}) // closes the overlay and the prompt left with nobody to answer
		switch {
		case err == nil:
			return "", nil
		case ctx.Err() != nil:
			return "", err
		case tgerr.Is(err, "SESSION_PASSWORD_NEEDED"):
			// QR taken by the phone, but the account has a 2FA password.
			pwd, err := authenticator{c: c}.Password(ctx)
			if err != nil {
				return "", err
			}
			if _, err := c.client.Auth().Password(ctx, pwd); err != nil {
				return "", fmt.Errorf(i18n.T("twofa_error"), err)
			}
			return "", nil
		}
		// The QR alone failed: the phone flow takes over.
		c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("qr_login_error", err)})
		return "", nil
	}
}

type authenticator struct {
	c *Client
	// phone : text already typed in front of the QR — given back as it is at the
	// first ask, otherwise the user would have to type their number again.
	phone string
}

func (a authenticator) ask(ctx context.Context, q string, secret bool) (string, error) {
	reply := make(chan string, 1)
	a.c.Post(model.EvAuthPrompt{Question: q, Secret: secret, Reply: reply})
	select {
	case s := <-reply:
		return strings.TrimSpace(s), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (a authenticator) Phone(ctx context.Context) (string, error) {
	if a.phone != "" {
		return a.phone, nil
	}
	return a.ask(ctx, i18n.T("prompt_phone"), false)
}

func (a authenticator) Password(ctx context.Context) (string, error) {
	return a.ask(ctx, i18n.T("prompt_2fa"), true)
}

func (a authenticator) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	return a.ask(ctx, i18n.T("prompt_code"), false)
}

func (a authenticator) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error { return nil }

func (a authenticator) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New(i18n.T("no_telegram_account"))
}
