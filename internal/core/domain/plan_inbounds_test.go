package domain

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

const (
	inA = "00000000-0000-7000-8000-0000000000a1"
	inB = "00000000-0000-7000-8000-0000000000b2"
	inC = "00000000-0000-7000-8000-0000000000c3"
)

func inboundPlan(in []store.PlanInbound, servers []string) *store.Plan {
	days := int32(30)
	return &store.Plan{NameI18n: map[string]string{"en": "I"}, Kind: "time", DurationDays: &days, Price: 1000,
		Currency: "IRT", Enabled: true, ServerIDs: servers, Inbounds: in}
}

// A plan's inbound choice is normalized (ids canonical, each inbound once,
// sorted, a server without inbounds dropped), bounded, and may only name a
// server the plan uses when it names any.
func TestPlanInboundsValidation(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	own := owner(t, svc)

	p, err := svc.UpsertPlan(ctx, own, inboundPlan([]store.PlanInbound{
		{ServerID: " " + "00000000-0000-7000-8000-0000000000B2", InboundIDs: []int32{3, 1, 3}},
		{ServerID: inA, InboundIDs: []int32{2}},
		{ServerID: inC}, // no inbound: the default for that server
		{ServerID: inB, InboundIDs: []int32{2}},
	}, nil))
	want := []store.PlanInbound{{ServerID: inA, InboundIDs: []int32{2}}, {ServerID: inB, InboundIDs: []int32{1, 2, 3}}}
	if err != nil || !reflect.DeepEqual(p.Inbounds, want) {
		t.Fatalf("plan: %v %+v", err, p.Inbounds)
	}

	tooMany := make([]int32, maxPlanInbounds+1)
	for i := range tooMany {
		tooMany[i] = int32(i + 1)
	}
	manyServers := make([]store.PlanInbound, maxPlanInboundServers+1)
	for i := range manyServers {
		manyServers[i] = store.PlanInbound{ServerID: inA, InboundIDs: []int32{1}}
	}
	for name, tc := range map[string]struct {
		in      []store.PlanInbound
		servers []string
	}{
		"not a server id":      {[]store.PlanInbound{{ServerID: "default", InboundIDs: []int32{1}}}, nil},
		"inbound 0":            {[]store.PlanInbound{{ServerID: inA, InboundIDs: []int32{0}}}, nil},
		"inbound negative":     {[]store.PlanInbound{{ServerID: inA, InboundIDs: []int32{1, -1}}}, nil},
		"too many inbounds":    {[]store.PlanInbound{{ServerID: inA, InboundIDs: tooMany}}, nil},
		"too many servers":     {manyServers, nil},
		"a server not in plan": {[]store.PlanInbound{{ServerID: inC, InboundIDs: []int32{1}}}, []string{inA, inB}},
	} {
		var fe *FieldError
		if _, err := svc.UpsertPlan(ctx, own, inboundPlan(tc.in, tc.servers)); !errors.As(err, &fe) || fe.Field != "inbounds" {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Exactly the maximum is fine, and a server the plan uses may have a choice.
	if _, err := svc.UpsertPlan(ctx, own, inboundPlan([]store.PlanInbound{{ServerID: inA, InboundIDs: tooMany[:maxPlanInbounds]}}, []string{inA})); err != nil {
		t.Fatalf("the maximum: %v", err)
	}
}

// A save without a list keeps the choice (the bot's and older clients'),
// an empty list clears it, and a server taken out of the plan takes its
// choice along.
func TestPlanInboundsKeptAndPruned(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	p, err := svc.UpsertPlan(ctx, own, inboundPlan([]store.PlanInbound{
		{ServerID: inA, InboundIDs: []int32{1}}, {ServerID: inB, InboundIDs: []int32{2, 3}}}, []string{inA, inB}))
	if err != nil {
		t.Fatal(err)
	}
	stored := func() []store.PlanInbound {
		t.Helper()
		got, err := s.GetPlan(ctx, s.Conn(), p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Inbounds
	}
	want := []store.PlanInbound{{ServerID: inA, InboundIDs: []int32{1}}, {ServerID: inB, InboundIDs: []int32{2, 3}}}
	if got := stored(); !reflect.DeepEqual(got, want) {
		t.Fatalf("stored: %+v", got)
	}
	// A price edit that carries neither servers nor inbounds.
	q := *p
	q.ServerIDs, q.Inbounds, q.Price = nil, nil, 900
	if got, err := svc.UpsertPlan(ctx, own, &q); err != nil || !reflect.DeepEqual(stored(), want) || got.Price != 900 {
		t.Fatalf("kept: %v %+v", err, stored())
	}
	// A server out of the plan (servers sent, inbounds not): its choice goes.
	q = *p
	q.ServerIDs, q.Inbounds = []string{inB}, nil
	if _, err := svc.UpsertPlan(ctx, own, &q); err != nil {
		t.Fatal(err)
	}
	if got := stored(); !reflect.DeepEqual(got, want[1:]) {
		t.Fatalf("server taken out: %+v", got)
	}
	// Servers and a choice sent together are checked together.
	q = *p
	q.ServerIDs, q.Inbounds = []string{inA}, []store.PlanInbound{{ServerID: inB, InboundIDs: []int32{2}}}
	var fe *FieldError
	if _, err := svc.UpsertPlan(ctx, own, &q); !errors.As(err, &fe) || fe.Field != "inbounds" {
		t.Fatalf("choice on a server taken out: %v", err)
	}
	// An empty list clears the choice.
	q = *p
	q.ServerIDs, q.Inbounds = nil, []store.PlanInbound{}
	if _, err := svc.UpsertPlan(ctx, own, &q); err != nil || len(stored()) != 0 {
		t.Fatalf("cleared: %v %+v", err, stored())
	}
	// The audit trail shows the choice.
	if e := auditJSON(t, s, "plan.upsert"); len(e) == 0 || !slices.ContainsFunc(e, func(x string) bool { return strings.Contains(x, `"inbound_ids"`) }) {
		t.Fatalf("plan.upsert audit: %v", e)
	}
}

// A purchase of a plan with an inbound choice reaches the provisioner's
// CreateClient with that choice; a plan without one sends none.
func TestPlanInboundsReachProvisioning(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	p, err := svc.UpsertPlan(ctx, own, inboundPlan([]store.PlanInbound{{ServerID: inB, InboundIDs: []int32{4, 2}}}, []string{inA, inB}))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = paidWalletOrder(t, svc, s, 9501, p)
	fp := &fakeProvisioner{}
	if worked, err := svc.NewProvisionWorker(fp, nil).ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("provision: %v %v", worked, err)
	}
	if !slices.Equal(fp.candidates, []string{inA, inB}) || !reflect.DeepEqual(fp.inbounds, []store.PlanInbound{{ServerID: inB, InboundIDs: []int32{2, 4}}}) {
		t.Fatalf("CreateClient got servers %v, inbounds %+v", fp.candidates, fp.inbounds)
	}
	// Cleared: the next purchase sends none.
	q := *p
	q.ServerIDs, q.Inbounds = nil, []store.PlanInbound{}
	if _, err := svc.UpsertPlan(ctx, own, &q); err != nil {
		t.Fatal(err)
	}
	_, _ = paidWalletOrder(t, svc, s, 9502, p)
	if worked, err := svc.NewProvisionWorker(fp, nil).ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("provision: %v %v", worked, err)
	}
	if len(fp.inbounds) != 0 {
		t.Fatalf("inbounds of a plan with no choice: %+v", fp.inbounds)
	}
}

// A choice on a server a plan does not name (the plan uses any server) is
// dropped when the server is deleted; the delete is audited with that.
func TestServerDeleteDropsInboundChoice(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	fs := &fakeServers{}
	de, err := svc.AddServer(ctx, own, fs, ServerInput{Name: "DE", BaseURL: "https://de.example.com", Token: "t", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.UpsertPlan(ctx, own, inboundPlan([]store.PlanInbound{
		{ServerID: de.ID, InboundIDs: []int32{1}}, {ServerID: inC, InboundIDs: []int32{2}}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteServer(ctx, own, fs, de.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := s.GetPlan(ctx, s.Conn(), p.ID)
	if err != nil || !reflect.DeepEqual(got.Inbounds, []store.PlanInbound{{ServerID: inC, InboundIDs: []int32{2}}}) {
		t.Fatalf("after the delete: %v %+v", err, got)
	}
}
