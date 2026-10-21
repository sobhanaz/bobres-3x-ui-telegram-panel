package domain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

func TestPlanGroupRules(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	owner := staffMember(t, s, 9601, "owner")
	days, gb := int32(30), int64(50<<30)
	newPlan := func(name string, mut func(*store.Plan)) *store.Plan {
		p := &store.Plan{NameI18n: map[string]string{"en": name}, Kind: "both", DurationDays: &days, TrafficBytes: &gb, Price: 1000, Currency: "IRT", Enabled: true}
		if mut != nil {
			mut(p)
		}
		got, err := svc.UpsertPlan(ctx, owner, p)
		if err != nil {
			t.Fatalf("plan %s: %v", name, err)
		}
		return got
	}
	invalid := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", what, err)
		}
	}

	// A group needs a name (either language), and its name and text have limits.
	_, err := svc.UpsertPlanGroup(ctx, owner, &store.PlanGroup{NameI18n: map[string]string{"en": "  "}})
	invalid("no name", err)
	_, err = svc.UpsertPlanGroup(ctx, owner, &store.PlanGroup{NameI18n: map[string]string{"en": strings.Repeat("n", 61)}})
	invalid("a long name", err)
	_, err = svc.UpsertPlanGroup(ctx, owner, &store.PlanGroup{NameI18n: map[string]string{"fa": "ن"}, DescriptionI18n: map[string]string{"fa": strings.Repeat("ن", 1501)}})
	invalid("a long text", err)
	_, err = svc.UpsertPlanGroup(ctx, owner, &store.PlanGroup{ID: "not-an-id", NameI18n: map[string]string{"en": "G"}})
	invalid("a bad id", err)
	if _, err = svc.UpsertPlanGroup(ctx, owner, &store.PlanGroup{ID: "00000000-0000-7000-8000-0000000008ff", NameI18n: map[string]string{"en": "G"}}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("editing a group that is not there: %v", err)
	}

	g, err := svc.UpsertPlanGroup(ctx, owner, &store.PlanGroup{NameI18n: map[string]string{"en": " Starter ", "fa": ""}, DescriptionI18n: map[string]string{"en": " Light use ", "fa": " "}, Sort: 3})
	if err != nil {
		t.Fatal(err)
	}
	if g.NameI18n["en"] != "Starter" || len(g.NameI18n) != 1 || g.DescriptionI18n["en"] != "Light use" || len(g.DescriptionI18n) != 1 || g.Sort != 3 {
		t.Fatalf("a saved group is trimmed and has no empty language: %+v", g)
	}

	// A plan in the group; the group counts it; a trial and a traffic package never have one.
	in := newPlan("Monthly", func(p *store.Plan) { p.GroupID = &g.ID })
	if in.GroupID == nil || *in.GroupID != g.ID {
		t.Fatalf("the plan's group: %v", in.GroupID)
	}
	trial := newPlan("Trial", func(p *store.Plan) { p.GroupID, p.IsTrial, p.Price = &g.ID, true, 0 })
	topup := newPlan("Extra", func(p *store.Plan) { p.GroupID, p.IsTopup, p.Kind, p.DurationDays = &g.ID, true, "traffic", nil })
	if trial.GroupID != nil || topup.GroupID != nil {
		t.Fatalf("a trial or a package in a group: %v %v", trial.GroupID, topup.GroupID)
	}
	if got, _ := svc.ListPlanGroups(ctx); len(got) != 1 || got[0].Plans != 1 {
		t.Fatalf("the group counts %+v", got)
	}

	// Saving the plan with no group keeps it; a group that does not exist is refused; "" takes it out.
	keep := *in
	keep.GroupID = nil
	if got := newPlan("Monthly", func(p *store.Plan) { *p = keep }); got.GroupID == nil || *got.GroupID != g.ID {
		t.Fatalf("a plan saved with no group lost it: %v", got.GroupID)
	}
	ghost, none := "00000000-0000-7000-8000-0000000008ee", ""
	keep.GroupID = &ghost
	_, err = svc.UpsertPlan(ctx, owner, &keep)
	invalid("a group that is not there", err)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Field != "group_id" {
		t.Errorf("the refusal names the field: %v", err)
	}
	keep.GroupID = &none
	if got := newPlan("Monthly", func(p *store.Plan) { *p = keep }); got.GroupID != nil {
		t.Fatalf("an empty group id did not take it out: %v", *got.GroupID)
	}

	// Deleting a group frees its plans; deleting it again is not found.
	keep.GroupID = &g.ID
	newPlan("Monthly", func(p *store.Plan) { *p = keep })
	if err := svc.DeletePlanGroup(ctx, owner, g.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeletePlanGroup(ctx, owner, g.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
	if err := svc.DeletePlanGroup(ctx, owner, "nonsense"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting nonsense: %v", err)
	}
	if got, err := s.GetPlan(ctx, s.Conn(), in.ID); err != nil || got.GroupID != nil || !got.Enabled {
		t.Fatalf("the plan after its group was deleted: %+v %v", got, err)
	}

	// At most fifty groups.
	for i := 0; i < maxPlanGroups; i++ {
		if _, err := svc.UpsertPlanGroup(ctx, owner, &store.PlanGroup{NameI18n: map[string]string{"en": "G"}}); err != nil {
			t.Fatalf("group %d: %v", i, err)
		}
	}
	_, err = svc.UpsertPlanGroup(ctx, owner, &store.PlanGroup{NameI18n: map[string]string{"en": "One too many"}})
	invalid("the 51st group", err)
}
