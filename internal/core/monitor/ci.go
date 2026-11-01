package monitor

import (
	"context"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/cicd"
)

// ciTimeout bounds one asking of the bot for the CI facts (the bot bounds its own look at GitHub to
// 45 seconds; core's client waits 50).
const ciTimeout = 55 * time.Second

// botStartGrace is how long after core starts a bot that does not answer is expected: an update or a reboot
// starts core a few seconds before the bot (whose name does not even resolve yet), so a failure then is logged
// as information, not as a warning the owner sees on the System page.
const botStartGrace = 2 * time.Minute

// ciStatus is the CI facts as last read from the bot (never blocks).
func (m *Monitor) ciStatus() cicd.Status {
	m.ciMu.Lock()
	defer m.ciMu.Unlock()
	st := m.ci
	if st.Runs == nil {
		st.Runs = []cicd.Run{}
	}
	if st.ReleaseRuns == nil {
		st.ReleaseRuns = []cicd.Run{}
	}
	if st.Releases == nil {
		st.Releases = []cicd.Release{}
	}
	return st
}

// ciVerdictKnown is whether the newest finished CI run on main failed, as last known (nil: never
// known). It survives a time GitHub or the bot cannot be reached, so an outage neither raises nor
// clears the alert.
func (m *Monitor) ciVerdictKnown() *bool {
	m.ciMu.Lock()
	defer m.ciMu.Unlock()
	return m.ciVerdict
}

// refreshCI asks the bot for the CI facts. A bot that does not answer leaves the earlier facts in
// place, marked with the error.
func (m *Monitor) refreshCI(ctx context.Context) {
	if m.cfg.Bot == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, ciTimeout)
	defer cancel()
	st, err := m.cfg.Bot.CI(ctx)
	m.ciMu.Lock()
	defer m.ciMu.Unlock()
	m.ciTried = m.now()
	if err != nil || st == nil {
		if m.ciTried.Sub(m.start) < botStartGrace {
			m.log.Info("monitor: CI facts (the bot is still starting)", "err", err)
		} else {
			m.log.Warn("monitor: CI facts", "err", err)
		}
		keep := m.ci
		keep.Error, keep.Detail = cicd.ErrUnreachable, "the bot did not answer"
		keep.Available = keep.CheckedAt > 0
		m.ci = keep
		return
	}
	m.ci, m.ciHave = *st, true
	if st.Available {
		if failed, _ := ciVerdict(*st); failed != nil {
			m.ciVerdict = failed
		}
	}
}
