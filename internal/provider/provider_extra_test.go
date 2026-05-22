package provider_test

import (
	"testing"

	"github.com/mmayadag/fx-rates/internal/provider"
)

func TestUnavailable_Error(t *testing.T) {
	u := &provider.Unavailable{Msg: "adapter is down"}
	if u.Error() != "adapter is down" {
		t.Fatalf("got %q", u.Error())
	}
}
