package dsc

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/govlog/ttyloom/internal/i18n"
)

// Remote authentication: the QR login of the official client. The gateway
// hands out a fingerprint to show as a QR; the phone that scans it sends the
// account, then a ticket once the user confirms; the ticket, posted to the
// API, gives back the token encrypted for the RSA key made here — nothing
// else ever carries it. Not documented by Discord; the same shape since 2019.
const (
	remoteAuthGateway = "wss://remote-auth-gateway.discord.gg/?v=2"
	remoteAuthLogin   = "https://discord.com/api/v9/users/@me/remote-auth/login"
	remoteAuthOrigin  = "https://discord.com" // the gateway refuses any other
	remoteAuthRA      = "https://discord.com/ra/"
)

// remoteAuth : one QR login. gateway and login are the two endpoints (the
// test replaces them); show gets the URL to draw as a QR and when it expires,
// scanned the name of the account seen on the phone — still to be confirmed.
type remoteAuth struct {
	gateway, login string
	show           func(url string, expires time.Time)
	scanned        func(user string)
	trace          func(string) // each op, both ways (the log window); nil = silent
}

func (r remoteAuth) log(format string, a ...any) {
	if r.trace != nil {
		r.trace(fmt.Sprintf(format, a...))
	}
}

// raMsg : every message of the gateway, both ways.
type raMsg struct {
	Op                   string `json:"op"`
	TimeoutMS            int    `json:"timeout_ms,omitempty"`
	HeartbeatInterval    int    `json:"heartbeat_interval,omitempty"`
	EncodedPublicKey     string `json:"encoded_public_key,omitempty"`
	EncryptedNonce       string `json:"encrypted_nonce,omitempty"`
	Proof                string `json:"proof,omitempty"` // client → server: SHA-256 of the nonce
	Fingerprint          string `json:"fingerprint,omitempty"`
	EncryptedUserPayload string `json:"encrypted_user_payload,omitempty"`
	Ticket               string `json:"ticket,omitempty"`
}

// run drives one login until the token, a refusal on the phone, the timeout
// of the gateway or ctx.
func (r remoteAuth) run(ctx context.Context) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", err
	}
	decrypt := func(b64 string) ([]byte, error) {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, err
		}
		return rsa.DecryptOAEP(sha256.New(), nil, key, raw, nil)
	}
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := d.DialContext(ctx, r.gateway, http.Header{"Origin": {remoteAuthOrigin}})
	if err != nil {
		return "", err
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }() // the reads have no context
	var wmu sync.Mutex                         // one writer at a time: the heartbeat runs beside
	send := func(m raMsg) error {
		wmu.Lock()
		defer wmu.Unlock()
		r.log("remote-auth → %s", m.Op)
		return conn.WriteJSON(m)
	}
	expires := time.Now().Add(2 * time.Minute) // what the gateway gives today; hello says
	shown := false
	for {
		var m raMsg
		if err := conn.ReadJSON(&m); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			r.log("remote-auth: %v", err)
			if shown { // the gateway closes when the code expires (five minutes today)
				return "", errors.New(i18n.T("qr_expired_discord"))
			}
			return "", err
		}
		r.log("remote-auth ← %s", m.Op)
		switch m.Op {
		case "hello":
			if m.TimeoutMS > 0 {
				expires = time.Now().Add(time.Duration(m.TimeoutMS) * time.Millisecond)
			}
			if m.HeartbeatInterval > 0 {
				go func() {
					t := time.NewTicker(time.Duration(m.HeartbeatInterval) * time.Millisecond)
					defer t.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-t.C:
							if send(raMsg{Op: "heartbeat"}) != nil {
								return
							}
						}
					}
				}()
			}
			if err := send(raMsg{Op: "init", EncodedPublicKey: base64.StdEncoding.EncodeToString(pub)}); err != nil {
				return "", err
			}
		case "nonce_proof":
			nonce, err := decrypt(m.EncryptedNonce)
			if err != nil {
				return "", err
			}
			sum := sha256.Sum256(nonce)
			if err := send(raMsg{Op: "nonce_proof", Proof: base64.RawURLEncoding.EncodeToString(sum[:])}); err != nil {
				return "", err
			}
		case "pending_remote_init":
			shown = true
			r.show(remoteAuthRA+m.Fingerprint, expires)
		case "pending_ticket":
			// id:discriminator:avatar:username — the name is all the UI shows.
			if p, err := decrypt(m.EncryptedUserPayload); err == nil {
				f := strings.Split(string(p), ":")
				r.scanned(f[len(f)-1])
			}
		case "pending_login":
			return r.token(ctx, m.Ticket, decrypt)
		case "cancel":
			return "", errors.New(i18n.T("qr_refused_discord"))
		}
	}
}

// token trades the ticket for the token: the one HTTP request of the flow.
func (r remoteAuth) token(ctx context.Context, ticket string, decrypt func(string) ([]byte, error)) (string, error) {
	body, _ := json.Marshal(map[string]string{"ticket": ticket})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.login, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("remote-auth login: HTTP %d", res.StatusCode)
	}
	var out struct {
		EncryptedToken string `json:"encrypted_token"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&out); err != nil {
		return "", err
	}
	tok, err := decrypt(out.EncryptedToken)
	if err != nil {
		return "", err
	}
	return string(tok), nil
}
