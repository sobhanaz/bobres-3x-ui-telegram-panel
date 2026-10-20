package handler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/runner"
	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sysupdate"
)

// staticReleases is the bot's check as core asks for it.
type staticReleases struct{ check coredomain.UpdateCheck }

func (s staticReleases) CheckUpdates(context.Context) (*coredomain.UpdateCheck, error) {
	c := s.check
	return &c, nil
}
func (s staticReleases) CheckUpdatesNow(ctx context.Context) (*coredomain.UpdateCheck, error) {
	return s.CheckUpdates(ctx)
}

// An update asked for in the dashboard reaches every owner on Telegram, in
// their own language, when it is requested and when it ends, and nobody else:
// the event core writes goes through the bot's own feed and notifier.
func TestUpdateFromTheDashboardReachesEveryOwner(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	second := w.person(9001, "sara")
	second.text("/start")
	second.press("lang:fa")
	w.role(9001, "owner")
	adm := w.person(861, "ali")
	adm.text("/start")
	adm.press("lang:en")
	w.role(861, "admin")

	dom := coredomain.New(w.stack.Store, coredomain.Config{OwnerTelegramIDs: []int64{ownerID}})
	dir := sysupdate.NewDir(t.TempDir())
	status := func(over map[string]any) {
		t.Helper()
		m := map[string]any{"v": 1, "installed": true, "helper": "0.7.0", "state": "idle", "id": "", "from": "", "to": "", "channel": "", "step": "",
			"message": "", "started_at": "", "finished_at": "", "log": []string{}}
		for k, v := range over {
			m[k] = v
		}
		raw, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(dir.Path(), sysupdate.StatusFile), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	status(nil)
	src := staticReleases{coredomain.UpdateCheck{Current: "0.6.1", Latest: "0.7.0", Channel: "stable", Newer: true, Verified: true, CheckedAt: 1760000000}}
	up := dom.NewUpdates(coredomain.UpdatesConfig{Source: src, Helper: dir, Version: "0.6.1"})
	actor, err := w.stack.Store.GetUserByTelegramID(context.Background(), w.stack.Store.Conn(), ownerID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := up.Apply(context.Background(), actor, coredomain.ApplyUpdate{Version: "0.7.0", Channel: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	w.drain()
	owner.eventuallySees("🔄 <b>Update requested</b>\nboss asked in the dashboard to update BOBRES from 0.6.1 to 0.7.0 (stable channel).")
	second.eventuallySees("🔄 <b>درخواست به‌روزرسانی</b>\nboss در داشبورد درخواست کرد")

	status(map[string]any{"state": "done", "id": run.ID, "from": "0.6.1", "to": "0.7.0", "channel": "stable", "step": "done",
		"started_at": time.Now().UTC().Format(time.RFC3339), "finished_at": time.Now().UTC().Format(time.RFC3339)})
	for range 3 { // once is enough; more does not repeat it
		if err := up.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	w.drain()
	owner.eventuallySees("✅ <b>Update finished</b>\nBOBRES now runs 0.7.0 (it was 0.6.1). Asked by boss.")
	second.eventuallySees("✅ <b>به‌روزرسانی انجام شد</b>")
	w.drain()
	if owner.count("Update finished") != 1 || owner.count("Update requested") != 1 || second.count("به‌روزرسانی انجام شد") != 1 {
		t.Fatalf("owner: %d finished, %d requested; second owner: %d finished", owner.count("Update finished"), owner.count("Update requested"), second.count("به‌روزرسانی انجام شد"))
	}
	// an admin is not an owner: nothing about it
	if adm.count("pdate") != 0 {
		t.Fatalf("an admin was told: %v", w.tg.all(861))
	}
}

// The channel the owner chooses in the dashboard is a setting like the others:
// the bot reads it when it reloads (at once when core asks), and its update
// check follows it, going back to BOBRES_UPDATE_CHANNEL when it is cleared.
func TestUpdateChannelSettingReachesTheBotsCheck(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	checker := release.NewChecker("0.6.1", "stable", nil)
	follow := runner.FollowUpdateChannel(checker, w.h.Settings().Get, "stable")
	reload := func() {
		t.Helper()
		if err := w.h.RefreshSettings(context.Background()); err != nil {
			t.Fatal(err)
		}
		follow()
	}
	reload()
	if checker.Channel() != "stable" {
		t.Fatalf("a fresh install: %q", checker.Channel())
	}
	w.setInCore("updates.channel", "beta")
	if checker.Channel() != "stable" {
		t.Fatal("the bot knew before it reloaded")
	}
	reload()
	if checker.Channel() != "beta" || checker.Status().Channel != "beta" {
		t.Fatalf("after the owner chose beta: %q %+v", checker.Channel(), checker.Status())
	}
	w.setInCore("updates.channel", "")
	reload()
	if checker.Channel() != "stable" {
		t.Fatalf("cleared: %q", checker.Channel())
	}
}
