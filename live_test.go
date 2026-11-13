package main

import (
	"context"
	"testing"
	"time"
)

func waitWire(t *testing.T, ch <-chan wire, want func(wire) bool) wire {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case msg := <-ch:
			if want(msg) {
				return msg
			}
		case <-timer.C:
			t.Fatal("timed out waiting for the shelf")
		}
	}
}

func TestLiveReachesEveryHand(t *testing.T) {
	s := testStore(t)
	live := NewLive(s)
	defer live.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := live.Subscribe(ctx)
	b := live.Subscribe(ctx)
	waitWire(t, a, func(m wire) bool { return m.Viewers == 2 })
	waitWire(t, b, func(m wire) bool { return m.Viewers == 2 })
	live.Announce("Two hands reached.")
	gotA := waitWire(t, a, func(m wire) bool { return m.Note == "Two hands reached." })
	gotB := waitWire(t, b, func(m wire) bool { return m.Note == "Two hands reached." })
	if gotA.Viewers != 2 || gotB.Viewers != 2 {
		t.Fatalf("viewers %d %d", gotA.Viewers, gotB.Viewers)
	}
	if len(gotA.Catalog.Products) != 7 {
		t.Fatalf("products %d", len(gotA.Catalog.Products))
	}
}

func TestSlowHandDoesNotBlockTheShelf(t *testing.T) {
	s := testStore(t)
	live := NewLive(s)
	defer live.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live.Subscribe(ctx)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			live.Announce("again")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a slow hand blocked the shelf")
	}
}

func TestSlipBackNote(t *testing.T) {
	back := diffNote(
		Catalog{LiveHolds: 1, Products: []Product{{ID: "pen", Name: "Fountain pen", Stock: 0, Held: 1, Initial: 1}}},
		Catalog{Products: []Product{{ID: "pen", Name: "Fountain pen", Stock: 1, Held: 0, Initial: 1}}},
	)
	if back != "Fountain pen is back on the shelf." {
		t.Fatal(back)
	}
	kept := diffNote(
		Catalog{Products: []Product{{ID: "ash", Name: "Ash bottles", Stock: 0, Held: 1, Initial: 1}}},
		Catalog{Products: []Product{{ID: "ash", Name: "Ash bottles", Stock: 0, Held: 0, Initial: 1}}},
	)
	if kept != "Ash bottles was kept." {
		t.Fatal(kept)
	}
	reset := diffNote(
		Catalog{Products: []Product{{ID: "ash", Name: "Ash bottles", Stock: 0, Held: 0, Initial: 1}}},
		Catalog{Products: []Product{{ID: "ash", Name: "Ash bottles", Stock: 1, Held: 0, Initial: 1}}},
	)
	if reset != "The shelf was set back." {
		t.Fatal(reset)
	}
}
