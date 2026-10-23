import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import PlansView from '../views/PlansView.vue'
import { setLang } from '../i18n'
import { state } from '../store'
import type { Plan } from '../api'
import { mount, settle } from './helpers'

const plan = (id: string, name: string, extra: Partial<Plan> = {}): Plan => ({
  id, names: { en: name }, kind: 'both', days: 30, traffic_bytes: 50 * 1024 ** 3, price: 150000, list_price: 150000,
  currency: 'IRT', scale: 0, topup: false, group: null, ...extra,
})

beforeEach(() => {
  setLang('en')
  state.plans = [plan('p1', 'Monthly'), plan('t1', 'Extra 10 GB', { topup: true })]
  state.groups = []
})
afterEach(() => {
  state.plans = null
  state.groups = []
})

// A renewal restarts the time and adds the traffic: the customer is told before choosing a plan to renew with.
describe('the renewal note', () => {
  it('explains how a renewal works when a plan is picked to renew, and only then', async () => {
    const renew = mount(PlansView, { mode: 'renew' }, ['pick'])
    await settle()
    expect(renew.text()).toContain('How renewal works')
    expect(renew.text()).toContain('the days left are dropped')
    expect(renew.text()).toContain("the plan's period counts from today")
    renew.unmount()

    for (const mode of ['new', 'traffic_topup']) {
      const v = mount(PlansView, { mode }, ['pick'])
      await settle()
      expect(v.text()).not.toContain('How renewal works')
      v.unmount()
    }
  })

  it('says it in Persian too', async () => {
    setLang('fa')
    const v = mount(PlansView, { mode: 'renew' }, ['pick'])
    await settle()
    expect(v.text()).toContain('نحوهٔ تمدید')
    expect(v.text()).toContain('روزهای باقی‌مانده صفر می‌شود')
    v.unmount()
    setLang('en')
  })
})
