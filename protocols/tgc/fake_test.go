package tgc

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/model"
)

// fakeInvoker : the seam under tg.Client. Every generated RPC boils down to
// rpc.Invoke(ctx, request, box) (tl_client_gen.go:35), so answering by request
// type drives the whole network layer of the package with no connection.
type fakeInvoker struct {
	answer func(req bin.Encoder) (any, error) // canned answer, by request type
	calls  []bin.Encoder                      // requests sent, in order
}

func (f *fakeInvoker) Invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	if f.answer == nil { // no answer set: the test expects no RPC at all
		return fmt.Errorf("fake invoker: unexpected %T", in)
	}
	// ponytail: unsynchronised append, one RPC at a time (Parallel threads=1,
	// read back after the event); add a mutex if a tested path ever fans out.
	f.calls = append(f.calls, in)
	v, err := f.answer(in)
	if err != nil {
		return err
	}
	return fill(out, v)
}

// fill puts v where gotd waits for its answer: a generated box holds one
// single field (the class of the answer, e.g. tg.UpdatesBox.Updates), a plain
// answer is the struct itself (e.g. *tg.OutboxReadDate).
func fill(out bin.Decoder, v any) error {
	dst, src := reflect.ValueOf(out).Elem(), reflect.ValueOf(v)
	switch {
	case src.Type().AssignableTo(dst.Type()):
		dst.Set(src)
	case src.Kind() == reflect.Pointer && src.Type().Elem().AssignableTo(dst.Type()):
		dst.Set(src.Elem())
	case dst.Kind() == reflect.Struct && dst.NumField() == 1 && src.Type().AssignableTo(dst.Field(0).Type()):
		dst.Field(0).Set(src)
	default:
		return fmt.Errorf("fake invoker: %T cannot answer %T", v, out)
	}
	return nil
}

// fakeClient : the gotd stack New builds (client.go:103-105) put on inv — same
// api, same peer manager, same sender, no connection. me, gaps and hook stay
// nil: none of the network methods under test reads them.
func fakeClient(inv tg.Invoker, events chan<- model.Event) *Client {
	api := tg.NewClient(inv)
	return &Client{api: api, peers: peers.Options{}.Build(api), sender: message.NewSender(api),
		dl: downloader.NewDownloader(), dlSem: make(chan struct{}, 3), ulSem: make(chan struct{}, 2),
		seen: map[int64]peers.Peer{}, Poster: model.Poster{Events: events}}
}

// next : the event the method under test posts. Every network method of the
// package runs in a goroutine of its own and answers on the channel: it is the
// only join point, and the UI waits on it the same way.
func next(t *testing.T, ev <-chan model.Event) model.Event {
	t.Helper()
	select {
	case e := <-ev:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("no event posted")
		return nil
	}
}
