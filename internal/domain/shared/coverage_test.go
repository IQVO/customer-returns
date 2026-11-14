package shared

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidTextBounds(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"plain", "Ceramic mug", true},
		{"empty", "", false},
		{"blank", "   ", false},
		{"leading space", " mug", false},
		{"trailing space", "mug ", false},
		{"control rune", "mug\x00", false},
		{"delete rune", "mug\x7f", false},
		{"tab inside", "a\tb", false},
		{"accents", "Cadeira de madeira", true},
		{"cjk", "磁器マグ", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidText(c.in); got != c.want {
				t.Fatalf("ValidText(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestValidTextNLimit(t *testing.T) {
	if !ValidTextN("12345", 5) {
		t.Fatal("five chars within a five-char limit must pass")
	}
	if ValidTextN("123456", 5) {
		t.Fatal("six chars beyond a five-char limit must fail")
	}
	if !ValidTextN("磁器マグ", 4) {
		t.Fatal("the limit counts runes, not bytes: four CJK runes pass a limit of 4")
	}
	if ValidText(strings.Repeat("a", MaxText+1)) {
		t.Fatal("one char beyond MaxText must fail")
	}
	if !ValidText(strings.Repeat("a", MaxText)) {
		t.Fatal("exactly MaxText chars pass")
	}
}

func TestHeaderAccessors(t *testing.T) {
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	h := Header{Version: 7, At: at}
	if h.AggregateVersion() != 7 {
		t.Fatalf("AggregateVersion = %d, want 7", h.AggregateVersion())
	}
	if !h.OccurredAt().Equal(at) {
		t.Fatalf("OccurredAt = %v, want %v", h.OccurredAt(), at)
	}
}

func TestErrorConstructors(t *testing.T) {
	if err := Corrupt("column %s", "node_id"); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("Corrupt must wrap ErrInvalidSnapshot: %v", err)
	}
	if err := Invalid("line %d", 3); !errors.Is(err, ErrValidation) {
		t.Fatalf("Invalid must wrap ErrValidation: %v", err)
	}
	if err := Illegal("%s from %s", "cancel", "CANCELLED"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("Illegal must wrap ErrIllegalTransition: %v", err)
	}
}
