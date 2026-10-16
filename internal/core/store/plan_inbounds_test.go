package store

import (
	"context"
	"reflect"
	"testing"
)

// A plan's inbound choice: read with the plan (empty = the default), replaced
// when a save carries a list, kept when it carries none, cleared by an empty
// one; a deleted plan takes it along, and a deleted server's rows are dropped.
func TestPlanInbounds(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := &Plan{NameI18n: map[string]string{"en": "I"}, Kind: "both", Price: 1, Currency: "IRT", Enabled: true}
	if err := s.UpsertPlan(ctx, s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	other := &Plan{NameI18n: map[string]string{"en": "O"}, Kind: "both", Price: 1, Currency: "IRT", Enabled: true,
		Inbounds: []PlanInbound{{ServerID: srvA, InboundIDs: []int32{9}}}}
	if err := s.UpsertPlan(ctx, s.Conn(), other); err != nil {
		t.Fatal(err)
	}
	get := func(id string) []PlanInbound {
		t.Helper()
		got, err := s.GetPlan(ctx, s.Conn(), id)
		if err != nil {
			t.Fatal(err)
		}
		return got.Inbounds
	}
	save := func(in []PlanInbound) {
		t.Helper()
		p.Inbounds = in
		if err := s.UpsertPlan(ctx, s.Conn(), p); err != nil {
			t.Fatal(err)
		}
	}
	if got := get(p.ID); got == nil || len(got) != 0 {
		t.Fatalf("a new plan: %#v", got)
	}

	save([]PlanInbound{{ServerID: srvB, InboundIDs: []int32{3, 1}}, {ServerID: srvA, InboundIDs: []int32{2}}})
	want := []PlanInbound{{ServerID: srvA, InboundIDs: []int32{2}}, {ServerID: srvB, InboundIDs: []int32{1, 3}}}
	if got := get(p.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("two servers: %+v", got)
	}
	list, err := s.ListPlans(ctx, s.Conn(), true)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %+v", err, list)
	}
	for _, lp := range list {
		if lp.ID == p.ID && !reflect.DeepEqual(lp.Inbounds, want) {
			t.Fatalf("listed: %+v", lp.Inbounds)
		}
	}

	// nil keeps the choice (an older client does not send it).
	p.Price = 2
	save(nil)
	if got := get(p.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("kept: %+v", got)
	}
	// Saved again as it is: nothing doubles, nothing is lost.
	save(want)
	if got := get(p.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("same again: %+v", got)
	}
	// One inbound goes, one comes, one server goes.
	save([]PlanInbound{{ServerID: srvB, InboundIDs: []int32{3, 4}}})
	if got := get(p.ID); !reflect.DeepEqual(got, []PlanInbound{{ServerID: srvB, InboundIDs: []int32{3, 4}}}) {
		t.Fatalf("edited: %+v", got)
	}
	// Another plan's rows are not touched by any of this.
	if got := get(other.ID); !reflect.DeepEqual(got, other.Inbounds) {
		t.Fatalf("another plan's choice: %+v", got)
	}
	// An empty list clears it.
	save([]PlanInbound{})
	if got := get(p.ID); len(got) != 0 {
		t.Fatalf("cleared: %+v", got)
	}
	// Refused: a server that is not an id, an inbound that is not positive.
	for name, in := range map[string][]PlanInbound{
		"not an id":   {{ServerID: "default", InboundIDs: []int32{1}}},
		"inbound 0":   {{ServerID: srvA, InboundIDs: []int32{0}}},
		"negative id": {{ServerID: srvA, InboundIDs: []int32{-2}}},
	} {
		p.Inbounds = in
		if err := s.UpsertPlan(ctx, s.Conn(), p); err == nil {
			t.Errorf("%s: stored", name)
		}
	}
	if got := get(p.ID); len(got) != 0 {
		t.Fatalf("a refused save changed the choice: %+v", got)
	}

	// A deleted server's rows go, for every plan, and only its own.
	save([]PlanInbound{{ServerID: srvA, InboundIDs: []int32{1, 2}}, {ServerID: srvB, InboundIDs: []int32{5}}})
	n, err := s.DeletePlanInboundsOfServer(ctx, s.Conn(), srvA)
	if err != nil || n != 2 {
		t.Fatalf("server delete: %d plans, %v", n, err)
	}
	if got := get(p.ID); !reflect.DeepEqual(got, []PlanInbound{{ServerID: srvB, InboundIDs: []int32{5}}}) {
		t.Fatalf("after the server went: %+v", got)
	}
	if got := get(other.ID); len(got) != 0 {
		t.Fatalf("another plan's rows of the server: %+v", got)
	}
	if n, err := s.DeletePlanInboundsOfServer(ctx, s.Conn(), srvA); err != nil || n != 0 {
		t.Fatalf("again: %d %v", n, err)
	}

	// A deleted plan takes its rows (no order holds it here).
	if _, err := s.Conn().Exec(ctx, `DELETE FROM core.plans WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := s.Conn().QueryRow(ctx, `SELECT count(*) FROM core.plan_inbounds WHERE plan_id = $1`, p.ID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("rows of a deleted plan: %d %v", left, err)
	}
}
