package main

import (
	"context"
	"sync"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
)

func TestIRCConfigCallbackRace(t *testing.T) {
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n := &config.IRCConfig{Name: "audit", Host: "localhost", Nick: "me"}
	cfg.IRC = []*config.IRCConfig{n}
	save := ircConfig(context.Background(), n, "", make(chan model.Event, 100)).SaveChannels
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			save([]string{"#one"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			cfg.Images = "off"
			cfg.Save()
			save([]string{"#two"})
		}
	}()
	wg.Wait()
}
