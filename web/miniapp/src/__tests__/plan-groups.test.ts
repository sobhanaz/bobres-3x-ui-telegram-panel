import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import PlansView from '../views/PlansView.vue'
import { setLang } from '../i18n'
import { state } from '../store'
import type { Plan, PlanGroup } from '../api'
import { mount, settle } from './helpers'

const plan = (id: string, name: string, group: string | null, extra: Partial<Plan> = {}): Plan => ({
  id, names: { en: name }, kind: 'both', days: 30, traffic_bytes: 50 * 1024 ** 3, price: 150000, list_price: 150000,
  currency: 'IRT', scale: 0, topup: false, group, ...extra,
})
const groups: PlanGroup[] = [
  { id: 'g1', names: { en: 'Starter', fa: 'شروع' }, texts: { en: 'For light use.\nNo refunds.', fa: 'برای مصرف سبک' } },
  { id: 'g2', names: { en: 'Nothing here' }, texts: {} },
]

beforeEach(() => {
  setLang('en')
  state.plans = [plan('p1', 'Weekly', 'g1'), plan('p2', 'Monthly', 'g1'), plan('p3', 'Quarterly', null), plan('t1', 'Extra 10 GB', null, { topup: true })]
  state.groups = groups
})
afterEach(() => {
  state.plans = null
  state.groups = []
})

describe('plan groups in the Mini App', () => {
  it('lists the groups first, then a group\'s text and plans, and Back returns to the groups', async () => {
    const v = mount(PlansView, {}, ['pick'])
    await settle()
    // the groups (one with a plan on sale) and Other plans; no plan yet
    expect(v.text()).toContain('Choose a category')
    v.button('Starter')
    v.button('Other plans')
    expect(v.text()).not.toContain('Nothing here')
    expect(v.text()).not.toContain('Weekly')

    await v.click('Starter')
    expect(v.text()).toContain('Starter')
    expect(v.text()).toContain('For light use.')
    expect(v.text()).toContain('Weekly')
    expect(v.text()).toContain('Monthly')
    expect(v.text()).not.toContain('Quarterly')
    // the text keeps its line break (pre-wrap), and tapping a price picks the plan
    expect(v.el.querySelector('.group-text')?.textContent).toBe('For light use.\nNo refunds.')
    await v.click(/150/)
    expect((v.events.pick[0][0] as Plan).id).toBe('p1')

    await v.click('All categories')
    expect(v.text()).toContain('Choose a category')

    await v.click('Other plans')
    expect(v.text()).toContain('Quarterly')
    expect(v.text()).not.toContain('Weekly')
    expect(v.text()).not.toContain('Extra 10 GB') // a traffic package is never a plan of a group
    v.unmount()
  })

  it('shows a store without groups the plans, as it always did', async () => {
    state.groups = []
    const v = mount(PlansView, {}, ['pick'])
    await settle()
    expect(v.text()).not.toContain('Choose a category')
    for (const name of ['Weekly', 'Monthly', 'Quarterly']) expect(v.text()).toContain(name)
    v.unmount()
  })

  it('lists renewals and traffic packages flat, whatever the groups', async () => {
    const renew = mount(PlansView, { mode: 'renew' }, ['pick'])
    await settle()
    expect(renew.text()).not.toContain('Choose a category')
    expect(renew.text()).toContain('Weekly')
    renew.unmount()
    const topup = mount(PlansView, { mode: 'traffic_topup' }, ['pick'])
    await settle()
    expect(topup.text()).toContain('Extra 10 GB')
    topup.unmount()
  })

  it('reads a group in the customer\'s language, else in any the group has', async () => {
    setLang('fa')
    state.groups = [{ id: 'g1', names: { fa: 'شروع' }, texts: { fa: 'فقط فارسی' } }]
    const v = mount(PlansView, {}, ['pick'])
    await settle()
    await v.click('شروع')
    expect(v.text()).toContain('فقط فارسی')
    v.unmount()
    setLang('en')
    const en = mount(PlansView, {}, ['pick'])
    await settle()
    await en.click('شروع')
    expect(en.text()).toContain('فقط فارسی')
    en.unmount()
  })
})
