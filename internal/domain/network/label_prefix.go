package network

// LabelPrefixLen is the length the label provider cuts a label's destination
// postal prefix at (the shipping context's ADR 0002 A10: "the first 5 letters
// or digits of the normalized postal code, the whole code when shorter").
const LabelPrefixLen = 5

// LabelPostalPrefix reduces a node's postal code to the destination prefix of
// a reverse label addressed to that node: the first LabelPrefixLen characters
// of the code after keeping only ASCII letters and digits, upper-cased; the
// whole remainder when it is shorter; "" when nothing is left.
//
// The rule is the label provider's, not this context's: the prefix is a field
// of the provider's route and is printed on the provider's label, so it is
// derived the way the provider derives the prefix of every other label
// (ADR 0005, addendum 2026-11-17). It is NOT the 3-character prefix
// customer-ordering publishes for an order, which is another field with
// another purpose (the region lookup). Only ASCII is kept, and a letter is
// never folded across the ASCII boundary, so the result always matches the
// route's `^[A-Z0-9]+$`.
func LabelPostalPrefix(code string) string {
	prefix := make([]byte, 0, LabelPrefixLen)
	for i := 0; i < len(code) && len(prefix) < LabelPrefixLen; i++ {
		c := code[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			prefix = append(prefix, c)
		}
	}
	return string(prefix)
}
