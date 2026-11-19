package network

import "testing"

// TestGroupStatusAuthorizable pins which order-group statuses may be returned:
// only SHIPPED and DELIVERED (a promised, lost or already returned group may not).
func TestGroupStatusAuthorizable(t *testing.T) {
	cases := []struct {
		status GroupStatus
		want   bool
	}{
		{GroupPromised, false},
		{GroupShipped, true},
		{GroupDelivered, true},
		{GroupLost, false},
		{GroupReturned, false},
		{GroupStatus(""), false},
		{GroupStatus("shipped"), false},
	}
	for _, c := range cases {
		t.Run(string(c.status), func(t *testing.T) {
			if got := c.status.Authorizable(); got != c.want {
				t.Fatalf("%q.Authorizable() = %v, want %v", c.status, got, c.want)
			}
		})
	}
}
