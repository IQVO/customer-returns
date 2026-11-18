package network

import "testing"

// TestLabelPostalPrefix pins the rule a node's postal code is reduced by
// before it is sent as the destination prefix of a reverse label: the label
// provider's own rule for a label prefix (shipping-and-tracking ADR 0002 A10,
// internal/application/shipto/shipto.go: the first 5 letters or digits of the
// normalized code, the whole code when shorter), over ASCII letters and digits
// only, so the value always fits the route's `^[A-Z0-9]+$`.
func TestLabelPostalPrefix(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{"the separator is dropped and the first five kept", "07750-000", "07750"},
		{"exactly five characters", "01310", "01310"},
		{"six characters keep the first five", "013101", "01310"},
		{"a short code is kept whole", "013", "013"},
		{"one character", "7", "7"},
		{"four characters", "1234", "1234"},
		{"lower case is upper-cased", "sw1a 1aa", "SW1A1"},
		{"every lower-case letter folds, a to z", "a-z", "AZ"},
		{"upper case and digits are kept as they are", "AZ09", "AZ09"},
		{"spaces and punctuation are dropped before the cut", " 0.7-7/5_0 0", "07750"},
		{"non-ASCII letters are dropped, never folded into ASCII", "é1ç2ñ3", "123"},
		{"a letter whose upper case would be ASCII is still dropped", "ı1ſ2", "12"},
		{"non-ASCII digits are dropped", "١٢٣45", "45"},
		{"the characters around the ASCII ranges are dropped", "/:@[`{", ""},
		{"the edges of the ASCII ranges are kept", "09AZaz", "09AZA"},
		{"nothing is left of a code without a letter or digit", "--- ---", ""},
		{"the empty code", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LabelPostalPrefix(c.code); got != c.want {
				t.Fatalf("LabelPostalPrefix(%q) = %q, want %q", c.code, got, c.want)
			}
		})
	}
}

func TestLabelPrefixLenIsTheProvidersFive(t *testing.T) {
	if LabelPrefixLen != 5 {
		t.Fatalf("LabelPrefixLen = %d: the label provider cuts a label prefix at 5 (its ADR 0002 A10)", LabelPrefixLen)
	}
}
