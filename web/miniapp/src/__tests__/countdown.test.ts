import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, type Ref } from 'vue'
import { monotonic, useCountdown, wall } from '../countdown'
import { mount, settle } from './helpers'

// The countdown of the price quote and of the USDT payment: whole seconds, by a timer of its own that reads the clock each tick.

interface Handle { left: Ref<number>; running: Ref<boolean>; start(at: number): void; stop(): void }

// A component that holds a countdown, so that it has a screen to go away with.
function holder(onDone: () => void, now?: () => number) {
  let handle!: Handle
  const component = defineComponent({
    setup() {
      handle = useCountdown(onDone, now)
      return () => h('p', String(handle.left.value))
    },
  })
  return { component, get: () => handle }
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(Date.UTC(2026, 9, 28, 12, 0, 0))
})
afterEach(() => {
  vi.useRealTimers()
  document.body.innerHTML = ''
})

describe('useCountdown', () => {
  it('counts whole seconds down to a deadline and runs onDone once, there', async () => {
    const done = vi.fn()
    const { component, get } = holder(done)
    const m = mount(component)
    get().start(monotonic() + 3)
    expect(get().left.value).toBe(3)
    expect(get().running.value).toBe(true)
    await settle(1000)
    expect(get().left.value).toBe(2)
    await settle(1000)
    expect(get().left.value).toBe(1)
    expect(done).not.toHaveBeenCalled()
    await settle(1000)
    expect(get().left.value).toBe(0)
    expect(get().running.value).toBe(false)
    expect(done).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(0)
    await settle(10_000)
    expect(done).toHaveBeenCalledTimes(1)
    m.unmount()
  })

  it('reads the clock at every tick, so a timer that was held back is right at its next one', async () => {
    const done = vi.fn()
    const { component, get } = holder(done)
    const m = mount(component)
    get().start(monotonic() + 60)
    // the browser sleeps the app for 45 seconds: time went by, but no tick ran
    vi.setSystemTime(Date.now() + 45_000)
    vi.advanceTimersByTime(45_000) // fires the ticks that were due, all at once
    expect(get().left.value).toBe(15)
    await settle(1000)
    expect(get().left.value).toBe(14)
    m.unmount()
  })

  it('does not follow the phone\'s date when it counts from now, and does when told a moment by it', async () => {
    const fromNow = holder(vi.fn())
    const a = mount(fromNow.component)
    fromNow.get().start(monotonic() + 100)
    const unix = holder(vi.fn(), wall)
    const b = mount(unix.component)
    unix.get().start(wall() + 100)
    expect(fromNow.get().left.value).toBe(100)
    expect(unix.get().left.value).toBe(100)
    vi.setSystemTime(Date.now() + 3600_000) // the phone's date is put an hour on
    await settle(1000)
    expect(fromNow.get().left.value).toBe(99) // a length of time went on as it was
    expect(unix.get().left.value).toBe(0) // a moment named by the date has passed by that date
    a.unmount()
    b.unmount()
  })

  it('does not start for a deadline that is already behind, and says nothing', async () => {
    const done = vi.fn()
    const { component, get } = holder(done)
    const m = mount(component)
    get().start(monotonic() - 5)
    expect(get().left.value).toBe(0)
    expect(get().running.value).toBe(false)
    expect(vi.getTimerCount()).toBe(0)
    await settle(5000)
    expect(done).not.toHaveBeenCalled()
    m.unmount()
  })

  it('starts over with a new deadline, and stops when told to, without running onDone', async () => {
    const done = vi.fn()
    const { component, get } = holder(done)
    const m = mount(component)
    get().start(monotonic() + 3)
    await settle(2000)
    get().start(monotonic() + 10) // one timer, not two
    expect(vi.getTimerCount()).toBe(1)
    expect(get().left.value).toBe(10)
    await settle(1000)
    expect(get().left.value).toBe(9)
    get().stop()
    expect(get().running.value).toBe(false)
    expect(vi.getTimerCount()).toBe(0)
    await settle(60_000)
    expect(done).not.toHaveBeenCalled()
    expect(get().left.value).toBe(9)
    m.unmount()
  })

  it('goes with the screen: unmounting leaves no timer and no call', async () => {
    const done = vi.fn()
    const { component, get } = holder(done)
    const m = mount(component)
    get().start(monotonic() + 5)
    expect(vi.getTimerCount()).toBe(1)
    m.unmount()
    expect(vi.getTimerCount()).toBe(0)
    await settle(60_000)
    expect(done).not.toHaveBeenCalled()
  })
})
