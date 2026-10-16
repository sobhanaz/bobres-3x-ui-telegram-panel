package provisionerclient

import (
	"slices"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// A plan's inbound choice goes to the provisioner as it is, a server with no
// inbound left out, and a plan with none sends nothing.
func TestInboundSelections(t *testing.T) {
	if got := inboundSelections(nil); got != nil {
		t.Fatalf("no choice: %+v", got)
	}
	in := []store.PlanInbound{{ServerID: "a", InboundIDs: []int32{2, 1}}, {ServerID: "b"}, {ServerID: "c", InboundIDs: []int32{7}}}
	got := inboundSelections(in)
	if len(got) != 2 || got[0].GetServerId() != "a" || !slices.Equal(got[0].GetInboundIds(), []int32{2, 1}) ||
		got[1].GetServerId() != "c" || !slices.Equal(got[1].GetInboundIds(), []int32{7}) {
		t.Fatalf("selections: %+v", got)
	}
	got[0].InboundIds[0] = 99
	if in[0].InboundIDs[0] != 2 {
		t.Fatal("the plan's ids were shared with the request")
	}
}
