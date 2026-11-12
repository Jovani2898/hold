package main

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "hold.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustProduct(t *testing.T, cat Catalog, id string) Product {
	t.Helper()
	for _, p := range cat.Products {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("missing %s", id)
	return Product{}
}

func TestTwoHoldsOnAshOneWins(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins, gone int
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Hold("ash")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
				return
			}
			if errors.Is(err, ErrGone) {
				gone++
				return
			}
			t.Error(err)
		}()
	}
	wg.Wait()
	if wins != 1 || gone != 1 {
		t.Fatalf("wins=%d gone=%d", wins, gone)
	}
	cat, err := s.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	ash := mustProduct(t, cat, "ash")
	if ash.Stock != 0 || ash.Held != 1 || cat.LiveHolds != 1 {
		t.Fatalf("stock=%d held=%d live=%d", ash.Stock, ash.Held, cat.LiveHolds)
	}
}
