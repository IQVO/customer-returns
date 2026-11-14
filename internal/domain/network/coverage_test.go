package network

import "testing"

func TestViewRegionOf(t *testing.T) {
	v := NewView(map[string]string{"03": "southeast", "84": "south"})
	if got := v.RegionOf("03"); got != "southeast" {
		t.Fatalf("RegionOf(03) = %q, want southeast", got)
	}
	if got := v.RegionOf("84"); got != "south" {
		t.Fatalf("RegionOf(84) = %q, want south", got)
	}
	if got := v.RegionOf("99"); got != "" {
		t.Fatalf("RegionOf(99) = %q, want the empty unknown", got)
	}
}

func TestGroupOriginOwnerOf(t *testing.T) {
	o := GroupOrigin{OwnersByItem: map[string]string{"sku-1": "sel-9"}}
	if got := o.OwnerOf("sku-1"); got != "sel-9" {
		t.Fatalf("OwnerOf(sku-1) = %q, want sel-9", got)
	}
	if got := o.OwnerOf("sku-2"); got != "" {
		t.Fatalf("OwnerOf(unknown) = %q, want empty", got)
	}
}
