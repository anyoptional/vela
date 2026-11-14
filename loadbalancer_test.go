package jinx

import (
	"errors"
	"testing"
)

func TestRoundRobin_Sequential(t *testing.T) {
	rr := newRoundRobin()
	candidates := []Instance{
		{ID: "c0"},
		{ID: "c1"},
		{ID: "c2"},
	}

	for i, want := range []string{"c0", "c1", "c2"} {
		got, err := rr.Choose(t.Context(), "orders", candidates)
		if err != nil {
			t.Fatalf("Choose() error = %v", err)
		}
		if got.ID != want {
			t.Errorf("Choose() call %d ID = %q, want %q", i, got.ID, want)
		}
	}
}

func TestRoundRobin_WrapAround(t *testing.T) {
	rr := newRoundRobin()
	candidates := []Instance{
		{ID: "c0"},
		{ID: "c1"},
		{ID: "c2"},
	}

	for range len(candidates) {
		if _, err := rr.Choose(t.Context(), "orders", candidates); err != nil {
			t.Fatalf("Choose() error = %v", err)
		}
	}

	got, err := rr.Choose(t.Context(), "orders", candidates)
	if err != nil {
		t.Fatalf("Choose() error = %v", err)
	}
	if got.ID != "c0" {
		t.Errorf("Choose() after one full cycle ID = %q, want %q", got.ID, "c0")
	}
}

func TestRoundRobin_PerServiceCounters(t *testing.T) {
	rr := newRoundRobin()
	candidates := []Instance{
		{ID: "c0"},
		{ID: "c1"},
	}
	tests := []struct {
		serviceID string
		want      string
	}{
		{serviceID: "orders", want: "c0"},
		{serviceID: "payments", want: "c0"},
		{serviceID: "orders", want: "c1"},
		{serviceID: "payments", want: "c1"},
	}
	for _, tt := range tests {
		got, err := rr.Choose(t.Context(), tt.serviceID, candidates)
		if err != nil {
			t.Fatalf("Choose(%q) error = %v", tt.serviceID, err)
		}
		if got.ID != tt.want {
			t.Errorf("Choose(%q) ID = %q, want %q", tt.serviceID, got.ID, tt.want)
		}
	}
}

func TestRoundRobin_Empty(t *testing.T) {
	rr := newRoundRobin()

	_, err := rr.Choose(t.Context(), "orders", nil)
	if !errors.Is(err, ErrNoInstances) {
		t.Errorf("Choose() error = %v, want %v", err, ErrNoInstances)
	}
}
