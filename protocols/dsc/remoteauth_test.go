package dsc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestRemoteAuth : the whole QR flow against a fake gateway and API — the key
// goes up, the nonce proof is right, the fingerprint becomes the QR URL, the
// scanning account is named, and the ticket comes back as the decrypted token.
func TestRemoteAuth(t *testing.T) {
	var mu sync.Mutex
	var pub *rsa.PublicKey
	enc := func(s string) string {
		mu.Lock()
		defer mu.Unlock()
		b, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, []byte(s), nil)
		if err != nil {
			t.Errorf("encrypt: %v", err)
		}
		return base64.StdEncoding.EncodeToString(b)
	}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/gateway", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "https://discord.com" {
			http.Error(w, "origin", http.StatusForbidden)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		c.WriteJSON(raMsg{Op: "hello", TimeoutMS: 120000, HeartbeatInterval: 41250})
		var m raMsg
		if c.ReadJSON(&m); m.Op != "init" {
			t.Errorf("first message: %+v", m)
			return
		}
		der, _ := base64.StdEncoding.DecodeString(m.EncodedPublicKey)
		k, err := x509.ParsePKIXPublicKey(der)
		if err != nil {
			t.Errorf("public key: %v", err)
			return
		}
		mu.Lock()
		pub = k.(*rsa.PublicKey)
		mu.Unlock()
		c.WriteJSON(raMsg{Op: "nonce_proof", EncryptedNonce: enc("nonce-1")})
		sum := sha256.Sum256([]byte("nonce-1"))
		if c.ReadJSON(&m); m.Op != "nonce_proof" || m.Proof != base64.RawURLEncoding.EncodeToString(sum[:]) {
			t.Errorf("nonce proof: %+v", m)
			return
		}
		c.WriteJSON(raMsg{Op: "pending_remote_init", Fingerprint: "fp1"})
		c.WriteJSON(raMsg{Op: "pending_ticket", EncryptedUserPayload: enc("1:0:hash:alice")})
		c.WriteJSON(raMsg{Op: "pending_login", Ticket: "tk"})
		c.ReadJSON(&m) // until the client leaves
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		if in["ticket"] != "tk" {
			http.Error(w, "ticket", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"encrypted_token": enc("tok-1")})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var shown, user string
	var expires time.Time
	ra := remoteAuth{gateway: "ws" + strings.TrimPrefix(srv.URL, "http") + "/gateway", login: srv.URL + "/login",
		show: func(u string, e time.Time) { shown, expires = u, e }, scanned: func(n string) { user = n }}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tok, err := ra.run(ctx)
	if err != nil || tok != "tok-1" {
		t.Fatalf("token: %q, %v", tok, err)
	}
	if shown != "https://discord.com/ra/fp1" || user != "alice" || expires.Before(time.Now().Add(time.Minute)) {
		t.Fatalf("shown %q, user %q, expires %v", shown, user, expires)
	}
}

// TestRemoteAuthLive : the real gateway, up to the fingerprint — no account
// is involved until a phone scans the code, so nothing is logged in. Skipped
// unless TTYLOOM_RA_LIVE is set: it needs the network and Discord's goodwill.
func TestRemoteAuthLive(t *testing.T) {
	if os.Getenv("TTYLOOM_RA_LIVE") == "" {
		t.Skip("TTYLOOM_RA_LIVE not set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var shown string
	ra := remoteAuth{gateway: remoteAuthGateway, login: remoteAuthLogin,
		show: func(u string, e time.Time) {
			shown = u
			t.Logf("QR shown, expires in %s", time.Until(e).Round(time.Second))
			cancel()
		},
		scanned: func(string) {}, trace: func(s string) { t.Log(s) }}
	if _, err := ra.run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("run: %v", err)
		return
	}
	if !strings.HasPrefix(shown, remoteAuthRA) || len(shown) <= len(remoteAuthRA) {
		t.Errorf("no fingerprint shown: %q", shown)
	}
}
