package registry

import "testing"

func TestAllReturnsECBOnly(t *testing.T) {
	entries := All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly one registry entry, got %d", len(entries))
	}
	if entries[0].Key != "ECB" {
		t.Fatalf("expected ECB entry, got %q", entries[0].Key)
	}
	if entries[0].Adapter == nil {
		t.Fatal("expected adapter to be set")
	}
}
