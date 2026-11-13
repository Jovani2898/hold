package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type wire struct {
	Viewers int     `json:"viewers"`
	Note    string  `json:"note,omitempty"`
	Catalog Catalog `json:"catalog"`
}

type join struct{ ch chan wire }
type leave struct{ ch chan wire }
type notice struct {
	note string
	done chan struct{}
}

type Live struct {
	store *Store
	cmds  chan any
	quit  chan struct{}
}

func NewLive(s *Store) *Live {
	l := &Live{
		store: s,
		cmds:  make(chan any, 32),
		quit:  make(chan struct{}),
	}
	go l.loop()
	return l
}

func (l *Live) Close() {
	select {
	case <-l.quit:
	default:
		close(l.quit)
	}
}

func (l *Live) Touch() { l.send("") }

func (l *Live) Announce(note string) { l.send(note) }

func (l *Live) send(note string) {
	done := make(chan struct{})
	select {
	case <-l.quit:
		return
	case l.cmds <- notice{note: note, done: done}:
	}
	select {
	case <-done:
	case <-l.quit:
	}
}

func (l *Live) Watch(ctx context.Context) {
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			l.Touch()
		}
	}
}

func (l *Live) Subscribe(ctx context.Context) <-chan wire {
	ch := make(chan wire, 4)
	select {
	case <-l.quit:
		close(ch)
		return ch
	case l.cmds <- join{ch: ch}:
	}
	go func() {
		<-ctx.Done()
		select {
		case <-l.quit:
		case l.cmds <- leave{ch: ch}:
		}
	}()
	return ch
}

func (l *Live) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := l.Subscribe(r.Context())
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			body, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", body)
			flusher.Flush()
		}
	}
}

func (l *Live) loop() {
	subs := map[chan wire]struct{}{}
	var last Catalog
	have := false
	for {
		select {
		case <-l.quit:
			for ch := range subs {
				close(ch)
			}
			return
		case raw := <-l.cmds:
			switch c := raw.(type) {
			case join:
				subs[c.ch] = struct{}{}
				if !have {
					if cat, err := l.store.Catalog(); err == nil {
						last = cat
						have = true
					}
				}
				offer(c.ch, wire{Viewers: len(subs), Catalog: last})
				fan(subs, wire{Viewers: len(subs), Catalog: last})
			case leave:
				if _, ok := subs[c.ch]; ok {
					delete(subs, c.ch)
					close(c.ch)
					fan(subs, wire{Viewers: len(subs), Catalog: last})
				}
			case notice:
				cat, err := l.store.Catalog()
				if err != nil {
					close(c.done)
					continue
				}
				note := c.note
				if note == "" && have {
					note = diffNote(last, cat)
				}
				changed := !have || note != "" || !sameShelf(last, cat)
				last = cat
				have = true
				if changed {
					fan(subs, wire{Viewers: len(subs), Note: note, Catalog: last})
				}
				close(c.done)
			}
		}
	}
}

func fan(subs map[chan wire]struct{}, msg wire) {
	for ch := range subs {
		offer(ch, msg)
	}
}

func offer(ch chan wire, msg wire) {
	select {
	case ch <- msg:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- msg:
	default:
	}
}

func sameShelf(a, b Catalog) bool {
	if a.LiveHolds != b.LiveHolds || len(a.Products) != len(b.Products) {
		return false
	}
	for i := range a.Products {
		if a.Products[i].ID != b.Products[i].ID || a.Products[i].Stock != b.Products[i].Stock || a.Products[i].Held != b.Products[i].Held {
			return false
		}
	}
	return true
}

func resting(cat Catalog) bool {
	if cat.LiveHolds != 0 || len(cat.Products) == 0 {
		return false
	}
	for _, p := range cat.Products {
		if p.Stock != p.Initial || p.Held != 0 {
			return false
		}
	}
	return true
}

func diffNote(before, after Catalog) string {
	prev := make(map[string]Product, len(before.Products))
	for _, p := range before.Products {
		prev[p.ID] = p
	}
	changes := 0
	restoredKept := false
	for _, p := range after.Products {
		old, ok := prev[p.ID]
		if !ok {
			continue
		}
		if p.Stock != old.Stock || p.Held != old.Held {
			changes++
		}
		if old.Held == 0 && old.Stock < old.Initial && p.Stock == p.Initial {
			restoredKept = true
		}
	}
	if len(before.Products) > 0 && (restoredKept || (changes > 1 && resting(after) && !resting(before))) {
		return "The shelf was set back."
	}
	note := ""
	for _, p := range after.Products {
		old, ok := prev[p.ID]
		if !ok {
			continue
		}
		var line string
		switch {
		case p.Held > old.Held:
			line = "A hand took " + p.Name + "."
		case p.Held < old.Held && p.Stock > old.Stock:
			line = p.Name + " is back on the shelf."
		case p.Held < old.Held:
			line = p.Name + " was kept."
		case p.Stock > old.Stock:
			line = p.Name + " is back on the shelf."
		}
		if line == "" {
			continue
		}
		if note != "" {
			note += " "
		}
		note += line
	}
	return note
}
