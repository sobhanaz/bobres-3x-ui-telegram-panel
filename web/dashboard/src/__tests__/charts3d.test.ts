import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import { i18n } from '../i18n'
import IsoColumns from '../components/charts3d/IsoColumns.vue'
import TiltDonut from '../components/charts3d/TiltDonut.vue'
import {
  backWall,
  boxFaces,
  clampCamera,
  depthOf,
  donutPoint,
  fit,
  frontWall,
  orderedByDepth,
  place,
  project,
  ringTop,
  shadeOf,
  visibleFaces,
  type Box,
} from '../components/charts3d/projection'

// The 3D charts are SVG polygons from plain arithmetic (components/charts3d/projection.ts): checked here as
// geometry (what is seen from where, in what order it is drawn), and as components (turning, picking, keys,
// the person's reduced-motion setting, the direction of the language).

const box = (x0: number, z1 = 1): Box => ({ x0, x1: x0 + 0.6, y0: 0, y1: 0.6, z0: 0, z1 })

describe('the camera', () => {
  it('looks straight at the front at yaw 0, and straight down at pitch 90', () => {
    const side = project(2, 1, 3, { yaw: 0, pitch: 0 })
    expect(side).toMatchObject({ x: 2, y: -3 }) // side on: x across, z up (screen y grows downward)
    const down = project(2, 1, 3, { yaw: 0, pitch: 90 })
    expect(down.x).toBeCloseTo(2)
    expect(down.y).toBeCloseTo(1) // from above the height cannot be seen, and nearer is lower on the screen
    const turned = project(1, 0, 0, { yaw: 90, pitch: 90 })
    expect(turned.x).toBeCloseTo(0)
    expect(turned.depth).toBeCloseTo(1)
  })

  it('sees the top and the faces that look toward it', () => {
    expect(visibleFaces({ yaw: 0, pitch: 34 })).toEqual(['top', 'front'])
    expect(visibleFaces({ yaw: 24, pitch: 34 })).toEqual(['top', 'front', 'right'])
    expect(visibleFaces({ yaw: -24, pitch: 34 })).toEqual(['top', 'front', 'left'])
    expect(visibleFaces({ yaw: 120, pitch: 34 })).toEqual(['top', 'back', 'right']) // from behind
    expect(visibleFaces({ yaw: 24, pitch: 90 })).toEqual(['top']) // from straight above no wall is seen
    expect(visibleFaces({ yaw: 24, pitch: 0 })).toEqual(['front', 'right']) // side on, no top
  })

  it('shades the top lightest, the front faces as they are and the side faces darker', () => {
    expect([shadeOf('top'), shadeOf('front'), shadeOf('back'), shadeOf('right'), shadeOf('left')]).toEqual(['top', 'light', 'light', 'dark', 'dark'])
  })

  it('turns a box into one four-cornered polygon per visible face', () => {
    const faces = boxFaces(box(0), { yaw: 24, pitch: 34 })
    expect(faces.map((f) => f.face)).toEqual(['top', 'front', 'right'])
    expect(faces.every((f) => f.points.length === 4)).toBe(true)
    expect(faces.every((f) => f.points.every((p) => Number.isFinite(p.x) && Number.isFinite(p.y)))).toBe(true)
    // the top is above the front on the screen
    const y = (f: string) => Math.max(...faces.find((x) => x.face === f)!.points.map((p) => p.y))
    expect(y('top')).toBeLessThan(y('front'))
  })

  it('draws far boxes before near ones, whichever way the scene is turned', () => {
    const row = [box(0), box(1), box(2), box(3)]
    expect(orderedByDepth(row, { yaw: 30, pitch: 34 })).toEqual([0, 1, 2, 3]) // the right end is nearer
    expect(orderedByDepth(row, { yaw: -30, pitch: 34 })).toEqual([3, 2, 1, 0]) // the left end is nearer
    expect(depthOf(box(3), { yaw: 30, pitch: 34 })).toBeGreaterThan(depthOf(box(0), { yaw: 30, pitch: 34 }))
    // equal depth keeps the given order (a stable draw)
    expect(orderedByDepth(row, { yaw: 0, pitch: 34 })).toEqual([0, 1, 2, 3])
  })

  it('fits a scene into the room it has, centred, and keeps its proportions', () => {
    const pad = { top: 10, right: 20, bottom: 30, left: 40 }
    const f = fit([{ x: 0, y: 0 }, { x: 10, y: 5 }], 340, 140, pad)
    expect(f.scale).toBe(20) // 280 / 10 across but only 100 / 5 down: the height is the limit
    const a = place({ x: 0, y: 0 }, f)
    const b = place({ x: 10, y: 5 }, f)
    expect(a.x).toBeGreaterThanOrEqual(pad.left)
    expect(b.x).toBeLessThanOrEqual(340 - pad.right)
    expect(a.y).toBeGreaterThanOrEqual(pad.top)
    expect(b.y).toBeLessThanOrEqual(140 - pad.bottom)
    expect(fit([], 100, 100, pad)).toEqual({ scale: 1, tx: 0, ty: 0 })
    // a flat scene (all points on a line) does not divide by zero
    expect(Number.isFinite(fit([{ x: 1, y: 1 }, { x: 1, y: 1 }], 100, 100, pad).scale)).toBe(true)
  })

  it('keeps the camera where the picture still reads', () => {
    expect(clampCamera({ yaw: 200, pitch: 5 })).toEqual({ yaw: 70, pitch: 18 })
    expect(clampCamera({ yaw: -200, pitch: 99 })).toEqual({ yaw: -70, pitch: 80 })
    expect(clampCamera({ yaw: 10, pitch: 40 })).toEqual({ yaw: 10, pitch: 40 })
  })
})

describe('the tilted donut', () => {
  const view = { rotation: 0, pitch: 90 }
  it('is a circle seen from above and an ellipse as it leans back', () => {
    const top = donutPoint(100, 100, 50, 0, view)
    expect(top.x).toBeCloseTo(100)
    expect(top.y).toBeCloseTo(50) // twelve o'clock
    const right = donutPoint(100, 100, 50, Math.PI / 2, view)
    expect(right.x).toBeCloseTo(150)
    expect(right.y).toBeCloseTo(100)
    const leaning = donutPoint(100, 100, 50, 0, { rotation: 0, pitch: 30 })
    expect(leaning.y).toBeCloseTo(75) // half as high: sin 30 degrees
    const turned = donutPoint(100, 100, 50, 0, { rotation: 90, pitch: 90 })
    expect(turned.x).toBeCloseTo(150) // the rotation moves where the first slice starts
    expect(donutPoint(100, 100, 50, 0, view, -7).y).toBeCloseTo(43) // a lifted slice
  })

  it('draws a slice as a closed ring sector, a whole turn as a ring, and nothing for no angle', () => {
    expect(ringTop(100, 100, 30, 50, 0, 1, view)).toMatch(/^M.*A50,50 0 0 1 .*A30,30 0 0 0 .*Z$/)
    expect(ringTop(100, 100, 30, 50, 0, Math.PI * 1.5, view)).toContain('A50,50 0 1 1')
    expect(ringTop(100, 100, 30, 50, 0, Math.PI * 2, view).match(/Z/g)).toHaveLength(2)
    expect(ringTop(100, 100, 30, 50, 1, 1, view)).toBe('')
    // leaning back squashes the arcs' vertical radius
    expect(ringTop(100, 100, 30, 50, 0, 1, { rotation: 0, pitch: 30 })).toContain('A50,25')
  })

  it('shows the outer wall only where it faces the viewer, and the inside of the hole only on the far side', () => {
    const lean = { rotation: 0, pitch: 56 }
    // a slice on the far half (right of twelve, going to three o'clock) has no outer wall to see
    expect(frontWall(100, 100, 50, 0.1, 1.2, lean, 12)).toBe('')
    expect(backWall(100, 100, 30, 0.1, 1.2, lean, 12)).not.toBe('')
    // a slice on the near half (round six o'clock) has one, and no inner wall
    expect(frontWall(100, 100, 50, 2.2, 4, lean, 12)).toMatch(/^M.*Z$/)
    expect(backWall(100, 100, 30, 2.2, 4, lean, 12)).toBe('')
    // seen from straight above the wall is as thin as it is deep: a strip of no height
    const strip = frontWall(100, 100, 50, 2.2, 4, lean, 12)
    expect(strip.length).toBeGreaterThan(20)
    // turning the donut half way swaps which slices are on the near side
    const half = { rotation: 180, pitch: 56 }
    expect(frontWall(100, 100, 50, 0.1, 1.2, half, 12)).not.toBe('')
    // a slice wider than a half turn can show two separate runs of wall
    expect(frontWall(100, 100, 50, 4.5, 4.5 + 5.6, lean, 12).match(/M/g)!.length).toBeGreaterThanOrEqual(1)
  })
})

// ---------------------------------------------------------------------------------- components

const apps: { unmount(): void; el: HTMLElement }[] = []
function mount(comp: Component, props: Record<string, unknown>, lang: 'en' | 'fa' = 'en') {
  i18n.global.locale.value = lang
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp({ render: () => h(comp, props) })
  app.use(i18n)
  app.mount(el)
  apps.push({ unmount: () => app.unmount(), el })
  return el
}
afterEach(() => {
  for (const a of apps.splice(0)) {
    a.unmount()
    a.el.remove()
  }
  vi.unstubAllGlobals()
  i18n.global.locale.value = 'fa'
})
const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
const all = (root: ParentNode, sel: string) => [...root.querySelectorAll<HTMLElement>(sel)]
const live = (el: HTMLElement) => text(el.querySelector('[aria-live]'))
const key = (el: Element, k: string) => el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }))
const setRange = async (el: HTMLInputElement, v: number) => {
  el.value = String(v)
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await nextTick()
}
/** mount, then let the first update draw what onMounted set up (the camera, the lean). */
async function mountSettled(comp: Component, props: Record<string, unknown>, lang: 'en' | 'fa' = 'en') {
  const el = mount(comp, props, lang)
  await nextTick()
  return el
}
const matchMedia = (reduce: boolean) =>
  vi.stubGlobal('matchMedia', (media: string) => ({ matches: media.includes('reduce') ? reduce : false, media, addEventListener() {}, removeEventListener() {} }))
const polys = (el: HTMLElement) => all(el, '.column polygon')
const xs = (poly: Element) => (poly.getAttribute('points') ?? '').split(' ').map((p) => Number(p.split(',')[0]))
const dated = ['1 Oct 2026', '2 Oct 2026', '3 Oct 2026', '4 Oct 2026']

describe('IsoColumns', () => {
  const props = {
    title: 'Revenue by plan',
    categories: dated,
    ticks: ['1', '2', '3', '4'],
    format: (n: number) => `${n} T`,
    series: [
      { key: 'a', label: 'Basic', values: [100, 250, 0, 400], slot: 0 },
      { key: 'b', label: 'Pro', values: [90, 200, 0, 380], slot: 1 },
    ],
    still: true,
    corner: 'Period',
  }

  it('draws a column for each period and the visible faces of each part of it', async () => {
    const el = await mountSettled(IsoColumns, props)
    expect(all(el, '.column')).toHaveLength(4)
    // six parts with a value (the third period is empty), three faces each at the home camera
    expect(polys(el)).toHaveLength(18)
    expect(el.querySelector('.scene')?.getAttribute('data-faces')).toBe('top front right')
    // the faces are shaded from the series colour: no hex, the theme's variable
    const fills = polys(el).map((p) => p.getAttribute('fill') ?? '')
    expect(fills.every((f) => f.includes('var(--chart-c1)') || f.includes('var(--chart-c2)'))).toBe(true)
    expect(fills.some((f) => f.includes('white'))).toBe(true) // the tops
    expect(fills.some((f) => f.includes('black'))).toBe(true) // the sides
    // the walls, the floor and the value rules are there to read it by
    expect(el.querySelectorAll('polygon.wall')).toHaveLength(2)
    expect(el.querySelectorAll('polygon.floor')).toHaveLength(1)
    expect(all(el, '.y-tick').length).toBeGreaterThan(2)
    expect(all(el, '.x-tick').map(text)).toEqual(['1', '2', '3', '4'])
  })

  it('turns with the two sliders, and shows the other side when turned the other way', async () => {
    const el = await mountSettled(IsoColumns, props)
    const [yaw, pitch] = all(el, 'input[type="range"]') as HTMLInputElement[]
    expect(text(el.querySelector('.controls label'))).toBe('Rotate')
    expect(yaw.value).toBe('34')
    await setRange(yaw, -30)
    expect(el.querySelector('.scene')?.getAttribute('data-faces')).toBe('top front left')
    await setRange(pitch, 80)
    expect(pitch.value).toBe('80')
    await setRange(yaw, 500) // the camera stays where the picture reads
    expect(Number(yaw.value)).toBeLessThanOrEqual(70)
    el.querySelector<HTMLButtonElement>('.reset')!.click()
    await nextTick()
    expect(yaw.value).toBe('34')
    expect(pitch.value).toBe('30')
  })

  it('picks a column with the keys and says it, dimming the others', async () => {
    const el = await mountSettled(IsoColumns, props)
    const scene = el.querySelector<HTMLElement>('.scene')!
    scene.focus()
    await nextTick()
    expect(live(el)).toBe('4 Oct 2026: Basic 400 T, Pro 380 T, Total 780 T')
    expect(all(el, '.column.on')).toHaveLength(1)
    expect(all(el, '.column.dim')).toHaveLength(3)
    key(scene, 'Home')
    await nextTick()
    expect(live(el)).toContain('1 Oct 2026')
    key(scene, 'Escape')
    await nextTick()
    expect(live(el)).toBe('')
    expect(all(el, '.column.dim')).toHaveLength(0)
    // hovering a column picks it too
    all(el, '.column')[1].dispatchEvent(new MouseEvent('pointerenter'))
    await nextTick()
    expect(text(el.querySelector('.tip .title'))).toBe('2 Oct 2026')
  })

  it('names itself, has its numbers as a table, and a legend that hides a plan', async () => {
    const el = await mountSettled(IsoColumns, { ...props, summary: 'Revenue by plan, 4 days' })
    expect(el.querySelector('.scene')?.getAttribute('aria-label')).toBe('Revenue by plan, 4 days')
    expect(el.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true')
    all(el, '.legend button.item')[0].click()
    await nextTick()
    expect(polys(el)).toHaveLength(9) // only the second plan's three parts remain
    el.querySelector<HTMLButtonElement>('.table-btn')!.click()
    await nextTick()
    expect(all(el, '.chart-table tbody tr')).toHaveLength(4)
    expect(text(mount(IsoColumns, { ...props, categories: [], series: [] }).querySelector('.empty'))).toBe('Nothing to show for this period.')
  })

  it('puts the first period at the right in Persian, with the scene turned the other way', async () => {
    // (the language is one setting for the whole page, so each is read before the other is mounted)
    const first = (el: HTMLElement) => Math.min(...xs(all(el, '.column')[0].querySelector('polygon')!))
    const last = (el: HTMLElement) => Math.min(...xs(all(el, '.column')[3].querySelector('polygon')!))
    const fa = await mountSettled(IsoColumns, props, 'fa')
    expect(fa.querySelector('.scene')?.getAttribute('data-faces')).toBe('top front left')
    expect(first(fa)).toBeGreaterThan(last(fa))
    expect(all(fa, '.y-tick')[0].getAttribute('text-anchor')).toBe('start') // the value labels sit on the start side
    const en = await mountSettled(IsoColumns, props, 'en')
    expect(en.querySelector('.scene')?.getAttribute('data-faces')).toBe('top front right')
    expect(first(en)).toBeLessThan(last(en))
    expect(all(en, '.y-tick')[0].getAttribute('text-anchor')).toBe('end')
  })

  it('turns into place once, unless the person asked for less motion', async () => {
    const raf = vi.fn((cb: FrameRequestCallback) => {
      void cb
      return 1
    })
    vi.stubGlobal('requestAnimationFrame', raf)
    vi.stubGlobal('cancelAnimationFrame', () => {})
    matchMedia(false)
    mount(IsoColumns, { ...props, still: false })
    await nextTick()
    expect(raf).toHaveBeenCalled()
    raf.mockClear()
    matchMedia(true) // prefers-reduced-motion: reduce
    const el = await mountSettled(IsoColumns, { ...props, still: false })
    expect(raf).not.toHaveBeenCalled()
    expect(el.querySelector<HTMLInputElement>('input[type="range"]')!.value).toBe('34') // already in place, no turning
  })
})

describe('TiltDonut', () => {
  const slices = [
    { key: 'w', label: 'Wallet', value: 600, slot: 0 },
    { key: 'g', label: 'Gateway', value: 300, slot: 1 },
    { key: 'm', label: 'Manual', value: 100, slot: 2 },
    { key: 'z', label: 'Nothing', value: 0 },
  ]
  const props = { title: 'Paid with', slices, format: (n: number) => `${n} T`, percent: (p: number) => `${Math.round(p)}%`, still: true }

  it('draws the top of each part and the wall of the ring, and lists every part with its value and share', async () => {
    const el = await mountSettled(TiltDonut, props)
    expect(all(el, 'path.top')).toHaveLength(3)
    expect(all(el, 'path.wall').length).toBeGreaterThan(0)
    expect(all(el, '.parts li').map((li) => [...li.querySelectorAll('.name, .value, .share')].map(text).join(' '))).toEqual(['Wallet 600 T 60%', 'Gateway 300 T 30%', 'Manual 100 T 10%'])
    expect(text(el.querySelector('.centre-value'))).toBe('1000 T')
    // the walls are darker shades of the part's colour
    expect(all(el, 'path.wall').every((w) => (w.getAttribute('fill') ?? '').includes('black'))).toBe(true)
    expect(all(el, 'path.top')[0].getAttribute('fill')).toContain('var(--chart-c1)')
    expect(text(mount(TiltDonut, { ...props, slices: [slices[3]], emptyText: 'No payments' }).querySelector('.empty'))).toBe('No payments')
  })

  it('turns and leans with the sliders (and no further than reads well)', async () => {
    const el = await mountSettled(TiltDonut, props)
    const [rot, tilt] = all(el, 'input[type="range"]') as HTMLInputElement[]
    expect(rot.value).toBe('0')
    expect(tilt.value).toBe('56')
    const before = el.querySelector('path.top')!.getAttribute('d')
    await setRange(rot, 90)
    expect(el.querySelector('path.top')!.getAttribute('d')).not.toBe(before)
    await setRange(tilt, 200)
    expect(Number(tilt.value)).toBeLessThanOrEqual(86)
    await setRange(tilt, 0)
    expect(Number(tilt.value)).toBeGreaterThanOrEqual(28)
    el.querySelector<HTMLButtonElement>('.reset')!.click()
    await nextTick()
    expect(rot.value).toBe('0')
    expect(tilt.value).toBe('56')
  })

  it('goes round the parts with the keys, lifting the one it is on and naming it in the centre', async () => {
    const el = await mountSettled(TiltDonut, props)
    const ring = el.querySelector<HTMLElement>('.ring')!
    ring.focus()
    await nextTick()
    expect(live(el)).toBe('Wallet: 600 T, 60%')
    expect(text(el.querySelector('.centre-label'))).toBe('Wallet')
    expect(all(el, 'svg g[transform="translate(0,-7)"]').length).toBeGreaterThan(0) // the slice stands out of the ring
    key(ring, 'ArrowRight')
    await nextTick()
    expect(live(el)).toBe('Gateway: 300 T, 30%')
    key(ring, 'Escape')
    await nextTick()
    expect(text(el.querySelector('.centre-value'))).toBe('1000 T')
    el.querySelector('path.top')!.dispatchEvent(new MouseEvent('pointerenter'))
    await nextTick()
    expect(text(el.querySelector('.tip .title'))).toBe('Wallet')
  })

  it('turns the other way in Persian, and leans back into place once unless motion is reduced', async () => {
    expect(mount(TiltDonut, props, 'fa').querySelector('svg g[transform*="scale(-1,1)"]')).not.toBeNull()
    expect(mount(TiltDonut, props, 'en').querySelector('svg g[transform*="scale(-1,1)"]')).toBeNull()
    const raf = vi.fn((cb: FrameRequestCallback) => {
      void cb
      return 1
    })
    vi.stubGlobal('requestAnimationFrame', raf)
    vi.stubGlobal('cancelAnimationFrame', () => {})
    matchMedia(true)
    const calm = await mountSettled(TiltDonut, { ...props, still: false })
    expect(raf).not.toHaveBeenCalled()
    expect((all(calm, 'input[type="range"]')[1] as HTMLInputElement).value).toBe('56') // already leaning back, not unfolding
    matchMedia(false)
    mount(TiltDonut, { ...props, still: false })
    await nextTick()
    expect(raf).toHaveBeenCalled()
  })
})
