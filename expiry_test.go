package main

import (
	"errors"
	"testing"
	"time"
)

func TestExpiredHoldReturnsConfirmedStays(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return at }

	held, err := s.Hold("ash")
	if err != nil {
		t.Fatal(err)
	}
	at = at.Add(31 * time.Second)
	cat, err := s.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	ash := mustProduct(t, cat, "ash")
	if ash.Stock != 1 || ash.Held != 0 {
		t.Fatalf("after expiry stock=%d held=%d", ash.Stock, ash.Held)
	}
	got, err := s.Get(held.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "expired" {
		t.Fatalf("status %s", got.Status)
	}

	again, err := s.Hold("ash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Confirm(again.ID); err != nil {
		t.Fatal(err)
	}
	at = at.Add(31 * time.Second)
	cat, err = s.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	ash = mustProduct(t, cat, "ash")
	if ash.Stock != 0 || cat.LiveHolds != 0 {
		t.Fatalf("confirmed stock=%d live=%d", ash.Stock, cat.LiveHolds)
	}
	got, err = s.Get(again.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "confirmed" {
		t.Fatalf("status %s", got.Status)
	}
	if _, err := s.Hold("ash"); !errors.Is(err, ErrGone) {
		t.Fatalf("second hold: %v", err)
	}
}
