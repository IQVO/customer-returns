package shared

import (
	"strings"
	"testing"
)

const (
	lower  = "abcdefghijklmnopqrstuvwxyz"
	upper  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digits = "0123456789"
)

// refAlnum is an independent statement of the alphanumeric class.
func refAlnum(c byte) bool {
	return strings.IndexByte(lower+upper+digits, c) >= 0
}

func TestValidIDBoundaries(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"single lower a", "a", true},
		{"single lower z", "z", true},
		{"single upper A", "A", true},
		{"single upper Z", "Z", true},
		{"single digit 0", "0", true},
		{"single digit 9", "9", true},
		{"mixed separators", "a.b_c:d-e", true},
		{"separator dot first", ".a", false},
		{"separator underscore first", "_a", false},
		{"separator colon first", ":a", false},
		{"separator dash first", "-a", false},
		{"separator only", "-", false},
		{"trailing separator", "a-", true},
		{"space inside", "a b", false},
		{"slash (just below 0)", "x/", false},
		{"at sign (just below A)", "x@", false},
		{"bracket (just above Z)", "x[", false},
		{"backtick (just below a)", "x`", false},
		{"brace (just above z)", "x{", false},
		{"non ascii", "caf\u00e9", false},
		{"exactly MaxID", strings.Repeat("a", MaxID), true},
		{"one over MaxID", strings.Repeat("a", MaxID+1), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidID(c.in); got != c.want {
				t.Fatalf("ValidID(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestValidIDEveryByte compares ValidID with an independent statement of the
// grammar for every possible byte, both as the first and as a later character.
func TestValidIDEveryByte(t *testing.T) {
	for b := 0; b < 256; b++ {
		c := byte(b)
		sep := c == '.' || c == '_' || c == ':' || c == '-'

		first := string([]byte{c})
		if got, want := ValidID(first), refAlnum(c); got != want {
			t.Fatalf("ValidID(first byte %#x) = %v, want %v", c, got, want)
		}
		later := string([]byte{'x', c})
		if got, want := ValidID(later), refAlnum(c) || sep; got != want {
			t.Fatalf("ValidID(x + byte %#x) = %v, want %v", c, got, want)
		}
	}
}

func TestValidOwnerRefBoundaries(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"single lower a", "a", true},
		{"single lower z", "z", true},
		{"single upper A", "A", true},
		{"single upper Z", "Z", true},
		{"single digit 0", "0", true},
		{"single digit 9", "9", true},
		{"dot first", ".a", true},
		{"underscore first", "_a", true},
		{"dash first", "-a", true},
		{"all separators", "._-", true},
		{"colon is not allowed", "sel:1", false},
		{"colon first", ":a", false},
		{"space inside", "sel 42", false},
		{"slash (just below 0)", "x/", false},
		{"at sign (just below A)", "x@", false},
		{"bracket (just above Z)", "x[", false},
		{"backtick (just below a)", "x`", false},
		{"brace (just above z)", "x{", false},
		{"exactly MaxID", strings.Repeat("a", MaxID), true},
		{"one over MaxID", strings.Repeat("a", MaxID+1), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidOwnerRef(c.in); got != c.want {
				t.Fatalf("ValidOwnerRef(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestValidOwnerRefEveryByte checks every byte in first and later position.
func TestValidOwnerRefEveryByte(t *testing.T) {
	for b := 0; b < 256; b++ {
		c := byte(b)
		want := refAlnum(c) || c == '.' || c == '_' || c == '-'
		for _, s := range []string{string([]byte{c}), string([]byte{'x', c})} {
			if got := ValidOwnerRef(s); got != want {
				t.Fatalf("ValidOwnerRef(%q) = %v, want %v", s, got, want)
			}
		}
	}
}
