import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import { i18n } from '../i18n'
import { BarChart, DonutChart, Heatmap, LineChart, Sparkline } from '../components/charts'

// The chart kit (components/charts/README.md) mounted on its own, in a real DOM: what it draws, what a pointer
// and the keyboard do, the states (empty, loading, refreshing), the table that stands in for the picture, the
// direction of the language, and that names from the data are never taken for markup.

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
  i18n.global.locale.value = 'fa'
})

const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
const all = (root: ParentNode, sel: string) => [...root.querySelectorAll<HTMLElement>(sel)]
const live = (el: HTMLElement) => text(el.querySelector('[aria-live]'))
/** a tooltip row or a part of a donut as its words, in order (the spans sit in separate boxes, so no space between them is text) */
const words = (row: Element) => [...row.querySelectorAll('.value, .share, .name, .label')].map(text).join(' ')
const key = (el: Element, k: string) => el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }))
const move = (svg: Element, clientX: number, clientY = 0) => svg.dispatchEvent(new MouseEvent('pointermove', { clientX, clientY, bubbles: true }))
const num = (n: number) => String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
const cats = ['Mon', 'Tue', 'Wed', 'Thu']
const dated = ['1 Oct 2026', '2 Oct 2026', '3 Oct 2026', '4 Oct 2026']
const two = [
  { key: 'a', label: 'Revenue', values: [100, 250, 0, 400], slot: 0, area: true },
  { key: 'b', label: 'Kept', values: [90, 200, 0, 380], slot: 2, dashed: true },
]
const base = { title: 'Revenue by day', categories: dated, ticks: cats, series: two, format: (n: number) => num(n) + ' T', axisFormat: (n: number) => num(n), corner: 'Period' }

describe('LineChart', () => {
  it('draws a line for each series, an area wash where asked, and dots on a short axis', () => {
    const el = mount(LineChart, base)
    expect(all(el, 'svg .series')).toHaveLength(2)
    const first = el.querySelector('svg .series')!
    expect(first.querySelectorAll('path')).toHaveLength(2) // the wash and the line
    expect(first.querySelectorAll('circle')).toHaveLength(4) // a dot on each of 4 points
    expect(el.querySelectorAll('svg .series')[1].querySelectorAll('path')).toHaveLength(1)
    expect(el.querySelector('svg .series')?.getAttribute('class')).toBe('series')
    // the colours are the theme's variables, never a hex
    expect(all(el, 'svg .series path[stroke]').map((p) => p.getAttribute('stroke'))).toEqual(['var(--chart-c1)', 'var(--chart-c3)'])
    // the second line is dashed (a second encoding beside its colour)
    expect(el.querySelectorAll('svg .series')[1].querySelector('path')?.getAttribute('stroke-dasharray')).toBeTruthy()
    // axis: round ticks on the value axis and the short names on the other
    expect(all(el, '.y-tick').map(text)).toEqual(['0', '100', '200', '300', '400'])
    expect(all(el, '.x-tick').map(text)).toEqual(cats)
  })

  it('lists the series in a legend whose buttons show and hide them, and keeps the last one', async () => {
    const el = mount(LineChart, base)
    const buttons = all(el, '.legend button.item')
    expect(buttons.map(text)).toEqual(['Revenue', 'Kept'])
    expect(buttons.map((b) => b.getAttribute('aria-pressed'))).toEqual(['true', 'true'])
    buttons[0].click()
    await nextTick()
    expect(all(el, 'svg .series')).toHaveLength(1)
    expect(buttons[0].getAttribute('aria-pressed')).toBe('false')
    buttons[1].click() // the last one visible stays: a chart is never blank by a click
    await nextTick()
    expect(all(el, 'svg .series')).toHaveLength(1)
    expect(buttons[1].getAttribute('aria-pressed')).toBe('true')
    buttons[0].click()
    await nextTick()
    expect(all(el, 'svg .series')).toHaveLength(2)
  })

  it('has no legend for one series: its title says what it is', () => {
    const el = mount(LineChart, { ...base, series: [two[0]] })
    expect(el.querySelector('.legend')).toBeNull()
  })

  it('follows the pointer: one tooltip lists every series at the nearest point', async () => {
    const el = mount(LineChart, base)
    const plot = el.querySelector('.plot svg')!
    const cx = Number(plot.querySelector('.series circle')!.getAttribute('cx')) // the first point
    move(plot, cx + 2)
    await nextTick()
    expect(text(el.querySelector('.tip .title'))).toBe('1 Oct 2026')
    expect(all(el, '.tip .row').map(words)).toEqual(['100 T Revenue', '90 T Kept'])
    expect(el.querySelector('.cursor')).not.toBeNull()
    move(plot, 100_000) // far to the end
    await nextTick()
    expect(text(el.querySelector('.tip .title'))).toBe('4 Oct 2026')
    // a finger that lifts has left the chart too, but what it touched stays picked until the next tap
    const touchLeave = new Event('pointerleave')
    Object.defineProperty(touchLeave, 'pointerType', { value: 'touch' })
    el.querySelector('.plot')!.dispatchEvent(touchLeave)
    await nextTick()
    expect(text(el.querySelector('.tip .title'))).toBe('4 Oct 2026')
    // a mouse that moves away takes the tooltip with it
    el.querySelector('.plot')!.dispatchEvent(new MouseEvent('pointerleave'))
    await nextTick()
    expect(el.querySelector('.tip')).toBeNull()
  })

  it('works from the keyboard, and says the point in a live region', async () => {
    const el = mount(LineChart, base)
    const plot = el.querySelector<HTMLElement>('.plot')!
    expect(plot.getAttribute('tabindex')).toBe('0')
    plot.focus()
    await nextTick()
    expect(live(el)).toBe('4 Oct 2026: Revenue 400 T, Kept 380 T') // focus lands on the latest point
    key(plot, 'ArrowLeft')
    await nextTick()
    expect(live(el)).toContain('3 Oct 2026')
    key(plot, 'Home')
    await nextTick()
    expect(live(el)).toContain('1 Oct 2026')
    key(plot, 'End')
    await nextTick()
    expect(live(el)).toContain('4 Oct 2026')
    key(plot, 'Escape')
    await nextTick()
    expect(live(el)).toBe('')
    expect(el.querySelector('.tip')).toBeNull()
  })

  it('names itself to screen readers, points at its instructions, and hides the drawing', () => {
    const el = mount(LineChart, { ...base, summary: 'Revenue peaked on 4 Oct' })
    const plot = el.querySelector('.plot')!
    expect(plot.getAttribute('role')).toBe('group')
    expect(plot.getAttribute('aria-roledescription')).toBe('chart')
    expect(plot.getAttribute('aria-label')).toBe('Revenue peaked on 4 Oct')
    const hint = document.getElementById(plot.getAttribute('aria-describedby')!)
    expect(text(hint)).toContain('arrow keys')
    expect(plot.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true')
    // without a summary the title names it
    expect(mount(LineChart, base).querySelector('.plot')?.getAttribute('aria-label')).toBe('Revenue by day')
  })

  it('stands in a table for the numbers, and goes back to the picture', async () => {
    const el = mount(LineChart, base)
    const btn = el.querySelector<HTMLButtonElement>('.table-btn')!
    expect(btn.getAttribute('aria-pressed')).toBe('false')
    btn.click()
    await nextTick()
    expect(btn.getAttribute('aria-pressed')).toBe('true')
    expect(all(el, '.chart-table thead th').map(text)).toEqual(['Period', 'Revenue', 'Kept'])
    expect(all(el, '.chart-table tbody tr').map((r) => all(r, 'th,td').map(text))).toEqual([
      ['1 Oct 2026', '100 T', '90 T'],
      ['2 Oct 2026', '250 T', '200 T'],
      ['3 Oct 2026', '0 T', '0 T'],
      ['4 Oct 2026', '400 T', '380 T'],
    ])
    expect(el.querySelector('.table-scroll')?.getAttribute('tabindex')).toBe('0') // scrollable by keyboard
    expect(el.querySelector<HTMLElement>('.body')?.style.display).toBe('none')
    btn.click()
    await nextTick()
    expect(el.querySelector('.chart-table')).toBeNull()
  })

  it('shows an empty state, a placeholder while the first data is on its way, and dims the old drawing while it refreshes', async () => {
    const empty = mount(LineChart, { ...base, categories: [], series: [] })
    expect(text(empty.querySelector('.empty'))).toBe('Nothing to show for this period.')
    expect(empty.querySelector('.plot')).toBeNull()
    expect(text(mount(LineChart, { ...base, categories: [], series: [], emptyText: 'No sales' }).querySelector('.empty'))).toBe('No sales')
    const waiting = mount(LineChart, { ...base, categories: [], series: [], loading: true })
    expect(waiting.querySelector('.skeleton')?.getAttribute('role')).toBe('status')
    expect(waiting.querySelector('.empty')).toBeNull()
    // data present and loading again: the picture stays (no jump) and is marked busy
    const refreshing = mount(LineChart, { ...base, loading: true })
    expect(refreshing.querySelector('.plot')).not.toBeNull()
    expect(refreshing.querySelector('figure')?.classList.contains('refreshing')).toBe(true)
    expect(refreshing.querySelector('figure')?.getAttribute('aria-busy')).toBe('true')
    expect(refreshing.querySelector('.skeleton')).toBeNull()
  })

  it('runs time from the start side: oldest at the left in English, at the right in Persian, and the arrows follow the screen', async () => {
    const en = mount(LineChart, base, 'en')
    const xs = (el: HTMLElement) => all(el, '.x-tick').map((t) => Number(t.getAttribute('x')))
    expect(xs(en)[0]).toBeLessThan(xs(en)[3])
    const fa = mount(LineChart, base, 'fa')
    expect(xs(fa)[0]).toBeGreaterThan(xs(fa)[3])
    // the value axis is on the start side: left in English, right in Persian
    const yx = (el: HTMLElement) => Number(el.querySelector('.y-tick')!.getAttribute('x'))
    expect(yx(en)).toBeLessThan(xs(en)[0])
    expect(yx(fa)).toBeGreaterThan(xs(fa)[0])
    // the right arrow goes right on screen: to the earlier point in Persian
    const plot = fa.querySelector<HTMLElement>('.plot')!
    plot.focus()
    key(plot, 'Home')
    key(plot, 'ArrowLeft')
    await nextTick()
    expect(live(fa)).toContain('2 Oct 2026')
    key(plot, 'ArrowRight')
    await nextTick()
    expect(live(fa)).toContain('1 Oct 2026')
    // a forced direction wins over the language
    expect(xs(mount(LineChart, { ...base, rtl: true }, 'en'))[0]).toBeGreaterThan(xs(mount(LineChart, { ...base, rtl: true }, 'en'))[3])
  })

  it('joins a point the range cuts to its neighbour with a dashed segment', () => {
    const el = mount(LineChart, { ...base, series: [two[1]], partial: [false, false, false, true] })
    const paths = all(el, 'svg .series path')
    expect(paths.length).toBeGreaterThan(1) // a solid run and a dashed one
    expect(paths.filter((p) => p.getAttribute('opacity') === '0.7')).toHaveLength(1)
    // a tooltip on the cut point says so
    const plot = el.querySelector('.plot')!
    plot.dispatchEvent(new FocusEvent('focus'))
    return nextTick().then(() => expect(text(el.querySelector('.tip .note'))).toBe('Only part of this period is inside the range.'))
  })

  it('stacks areas of parts of a whole and adds the total to the tooltip', async () => {
    const el = mount(LineChart, { ...base, stacked: true, series: two.map((s) => ({ ...s, dashed: false })) })
    expect(all(el, 'svg .series path[fill-opacity="0.55"]')).toHaveLength(2) // two filled bands
    const plot = el.querySelector<HTMLElement>('.plot')!
    plot.focus()
    await nextTick()
    expect(all(el, '.tip .row').map(words)).toEqual(['400 T Revenue', '380 T Kept', '780 T Total'])
    // the value axis reaches the stack's height, not the tallest single series
    expect(Math.max(...all(el, '.y-tick').map((t) => Number(text(t))))).toBeGreaterThanOrEqual(780)
  })

  it('draws a fixed vertical range when asked, a fitted one with a non-zero baseline, and negative values', () => {
    const pct = mount(LineChart, { ...base, series: [{ key: 'cpu', label: 'CPU', values: [20, 40, 35, 30] }], yMin: 0, yMax: 100 })
    expect(all(pct, '.y-tick').map(text)).toEqual(['0', '25', '50', '75', '100'])
    const fitted = mount(LineChart, { ...base, series: [{ key: 'cpu', label: 'CPU', values: [60, 64, 62, 66] }], baseline: 'auto' })
    expect(Math.min(...all(fitted, '.y-tick').map((t) => Number(text(t))))).toBeGreaterThan(0)
    const neg = mount(LineChart, { ...base, series: [{ key: 'n', label: 'Net', values: [-50, 30, -10, 80] }] })
    expect(all(neg, '.y-tick').map((t) => Number(text(t)))).toEqual([-50, 0, 50, 100])
    expect(neg.querySelector('.grid line.zero')).not.toBeNull()
  })

  it('draws one point as a dot, and a long axis without dots and with thinned labels', () => {
    const one = mount(LineChart, { ...base, categories: ['1 Oct'], ticks: ['1 Oct'], series: [{ key: 'a', label: 'A', values: [5] }] })
    expect(one.querySelectorAll('svg .series circle')).toHaveLength(1)
    const n = 200
    const long = mount(LineChart, { ...base, categories: Array.from({ length: n }, (_, i) => `d${i}`), ticks: Array.from({ length: n }, (_, i) => `d${i}`), series: [{ key: 'a', label: 'A', values: Array.from({ length: n }, (_, i) => i) }] })
    expect(long.querySelectorAll('svg .series circle').length).toBeLessThan(3) // only the end dot
    expect(all(long, '.x-tick').length).toBeLessThan(12)
    expect(Number(long.querySelector('.plot svg')!.getAttribute('width'))).toBeLessThanOrEqual(640)
  })

  it('writes names from the data as text, never as markup', async () => {
    const evil = '<img src=x onerror=alert(1)><b>x</b>'
    const el = mount(LineChart, { ...base, categories: [evil, 'b', 'c', 'd'], series: [{ key: 'a', label: evil, values: [1, 2, 3, 4] }, { key: 'b', label: 'B', values: [1, 2, 3, 4] }] })
    const plot = el.querySelector<HTMLElement>('.plot')!
    plot.focus()
    key(plot, 'Home')
    el.querySelector<HTMLButtonElement>('.table-btn')!.click()
    await nextTick()
    expect(el.querySelector('img')).toBeNull()
    expect(el.querySelector('b')).toBeNull()
    expect(text(el.querySelector('.legend'))).toContain(evil)
    expect(text(el.querySelector('.chart-table tbody th'))).toBe(evil)
  })
})

describe('BarChart', () => {
  const stacked = {
    title: 'Revenue by plan', categories: dated, ticks: cats, mode: 'stacked', format: (n: number) => num(n), corner: 'Period',
    series: [
      { key: 'p1', label: 'Basic', values: [10, 20, 0, 30], slot: 0 },
      { key: 'p2', label: 'Pro', values: [5, 0, 0, 10], slot: 1 },
      { key: 'p3', label: 'Other', values: [0, 5, 0, 0], slot: 'other' },
    ],
  }

  it('draws a bar for each value, none for zero, and rounds only the top of a stack', () => {
    const el = mount(BarChart, stacked)
    const bars = all(el, 'path.bar')
    expect(bars).toHaveLength(6) // every non-zero value of every series: 3 + 2 + 1
    // one rounded top per non-empty column (the top-most segment); the segments below it are square
    expect(bars.filter((b) => b.getAttribute('d')!.includes('A'))).toHaveLength(3)
    expect(bars.find((b) => b.getAttribute('fill') === 'var(--chart-c1)' && !b.getAttribute('d')!.includes('A'))).toBeDefined()
    // a stack's segments are separated by a gap of the surface colour
    expect(bars[0].getAttribute('stroke')).toBe('var(--chart-surface)')
    expect(bars[0].getAttribute('stroke-width')).toBe('1.5')
    // the neutral grey is for "other"
    expect(bars.some((b) => b.getAttribute('fill') === 'var(--chart-other)')).toBe(true)
  })

  it('never makes a bar thicker than 28 px, whatever the room', () => {
    const el = mount(BarChart, { title: 'One', categories: ['a'], series: [{ key: 's', label: 'S', values: [5] }], format: String })
    const d = el.querySelector('path.bar')!.getAttribute('d')!
    // the path is M x,y V .. A r,r .. H (the right edge less the radius, r = 4) ..: its width is H + 4 - x
    const width = Number(/H([\d.-]+)/.exec(d)![1]) - Number(/^M([\d.-]+),/.exec(d)![1]) + 4
    expect(width).toBeLessThanOrEqual(28)
    expect(width).toBeGreaterThan(20)
  })

  it('groups bars side by side with their own colours when not stacked', () => {
    const el = mount(BarChart, { ...stacked, mode: 'grouped' })
    expect(all(el, 'path.bar')).toHaveLength(6)
    expect(all(el, 'path.bar').every((b) => b.getAttribute('stroke-width') === '0')).toBe(true)
    // every bar of a group is rounded at the end that carries the value
    expect(all(el, 'path.bar').every((b) => b.getAttribute('d')!.includes('A'))).toBe(true)
    expect(new Set(all(el, 'path.bar').map((b) => b.getAttribute('fill'))).size).toBe(3)
  })

  it('draws a negative value below the zero line, rounded at its end', () => {
    const el = mount(BarChart, { title: 'Net', categories: ['a', 'b'], series: [{ key: 'n', label: 'Net', values: [40, -30] }], format: String })
    expect(all(el, 'path.bar')).toHaveLength(2)
    expect(el.querySelector('.grid line.zero')).not.toBeNull()
    expect(all(el, '.y-tick').map((t) => Number(text(t))).some((v) => v < 0)).toBe(true)
  })

  it('lights up the picked category and lists every series of it in one tooltip, with the total of a stack', async () => {
    const el = mount(BarChart, stacked)
    const svg = el.querySelector('.plot svg')!
    const first = all(el, 'path.bar')[0].getAttribute('d')!
    move(svg, Number(/^M([\d.-]+),/.exec(first)![1]) + 3)
    await nextTick()
    expect(text(el.querySelector('.tip .title'))).toBe('1 Oct 2026')
    expect(all(el, '.tip .row').map(words)).toEqual(['10 Basic', '5 Pro', '0 Other', '15 Total'])
    expect(el.querySelector('rect.wash')).not.toBeNull()
    const plot = el.querySelector<HTMLElement>('.plot')!
    plot.focus()
    key(plot, 'Home')
    key(plot, 'ArrowRight')
    await nextTick()
    expect(live(el)).toBe('2 Oct 2026: Basic 20, Pro 0, Other 5, Total 25')
  })

  it('draws a column that holds only part of its period lighter, and says so', async () => {
    const el = mount(BarChart, { ...stacked, partial: [false, false, false, true] })
    expect(all(el, 'path.bar').filter((b) => b.getAttribute('opacity') === '0.55').length).toBe(2)
    const plot = el.querySelector<HTMLElement>('.plot')!
    plot.focus() // lands on the last (partial) column
    await nextTick()
    expect(text(el.querySelector('.tip .note'))).toContain('Only part of this period')
  })

  it('shows a ranking as horizontal bars with the value written at the tip', () => {
    const el = mount(BarChart, {
      title: 'Top plans', orientation: 'horizontal', categories: ['Gold 90 days', 'Basic', 'A rather long plan name that cannot fit in the label column'],
      series: [{ key: 'r', label: 'Revenue', values: [900, 500, 100], slot: 0 }], format: (n: number) => num(n) + ' T',
    })
    expect(all(el, 'path.bar')).toHaveLength(3)
    expect(all(el, '.value-text text').map(text)).toEqual(['900 T', '500 T', '100 T'])
    const labels = all(el, '.row-label').map(text)
    expect(labels.slice(0, 2)).toEqual(['Gold 90 days', 'Basic'])
    expect(labels[2].endsWith('…')).toBe(true) // the long name is shortened, its full name is in the tooltip
    // the longest bar is the longest value, and they all start at the baseline
    const widths = all(el, 'path.bar').map((b) => {
      const d = b.getAttribute('d')!
      return { start: Number(/^M([\d.-]+),/.exec(d)![1]), end: Number(/H([\d.-]+)/.exec(d)![1]) }
    })
    expect(new Set(widths.map((w) => w.start)).size).toBe(1)
    expect(widths[0].end).toBeGreaterThan(widths[1].end)
    // in Persian the bars grow from the right and the value labels sit on their left
    const fa = mount(BarChart, { title: 'Top plans', orientation: 'horizontal', categories: ['الف', 'ب'], series: [{ key: 'r', label: 'درآمد', values: [900, 500] }], format: String }, 'fa')
    expect(fa.querySelector('.value-text text')?.getAttribute('text-anchor')).toBe('end')
    expect(fa.querySelector('.row-label')?.getAttribute('text-anchor')).toBe('start')
    expect(el.querySelector('.row-label')?.getAttribute('text-anchor')).toBe('end') // and the other way round in English
  })

  it('thins a long axis, and mirrors in Persian', () => {
    const n = 90
    const cs = Array.from({ length: n }, (_, i) => `d${i}`)
    const el = mount(BarChart, { title: 'Long', categories: cs, series: [{ key: 'a', label: 'A', values: cs.map((_, i) => i % 7) }], format: String })
    expect(all(el, '.x-tick').length).toBeLessThan(14)
    expect(all(el, 'path.bar').length).toBeGreaterThan(70)
    const en = mount(BarChart, stacked, 'en')
    const fa = mount(BarChart, stacked, 'fa')
    const first = (e: HTMLElement) => Number(all(e, '.x-tick')[0].getAttribute('x'))
    expect(first(fa)).toBeGreaterThan(first(en))
  })

  it('has the table, the legend toggle and the states of every chart', async () => {
    const el = mount(BarChart, stacked)
    el.querySelector<HTMLButtonElement>('.table-btn')!.click()
    await nextTick()
    expect(all(el, '.chart-table thead th').map(text)).toEqual(['Period', 'Basic', 'Pro', 'Other'])
    el.querySelector<HTMLButtonElement>('.table-btn')!.click()
    await nextTick()
    all(el, '.legend button.item')[0].click()
    await nextTick()
    expect(all(el, 'path.bar')).toHaveLength(3) // Basic is hidden: Pro and Other remain
    expect(text(mount(BarChart, { ...stacked, categories: [], series: [] }).querySelector('.empty'))).toBe('Nothing to show for this period.')
    expect(mount(BarChart, { ...stacked, categories: [], series: [], loading: true }).querySelector('.skeleton')).not.toBeNull()
  })
})
describe('DonutChart', () => {
  const slices = [
    { key: 'w', label: 'Wallet', value: 600, slot: 0 },
    { key: 'g', label: 'Gateway', value: 300, slot: 1 },
    { key: 'm', label: 'Manual', value: 100, slot: 2 },
    { key: 'z', label: 'Nothing', value: 0, slot: 3 },
  ]
  const props = { title: 'Paid with', slices, format: (n: number) => num(n) + ' T', percent: (p: number) => `${Math.round(p)}%`, corner: 'Method' }

  it('draws a slice for each part with a value, and writes every part with its value and share', () => {
    const el = mount(DonutChart, props)
    expect(all(el, 'path.slice')).toHaveLength(3)
    expect(all(el, '.parts li').map(words)).toEqual(['Wallet 600 T 60%', 'Gateway 300 T 30%', 'Manual 100 T 10%'])
    expect(all(el, 'path.slice').map((p) => p.getAttribute('fill'))).toEqual(['var(--chart-c1)', 'var(--chart-c2)', 'var(--chart-c3)'])
    expect(text(el.querySelector('.centre-value'))).toBe('1,000 T')
    expect(text(el.querySelector('.centre-label'))).toBe('Total')
    expect(el.querySelector('.legend')).toBeNull() // the list is the legend
  })

  it('writes the total short enough for the hole, in a smaller size the longer it is', () => {
    const short = mount(DonutChart, { ...props, centerFormat: (n: number) => `${n / 1000}K` })
    expect(text(short.querySelector('.centre-value'))).toBe('1K')
    const longer = mount(DonutChart, { ...props, format: (n: number) => `${n * 100_000} Toman` })
    const size = (e: HTMLElement) => parseFloat(e.querySelector<HTMLElement>('.centre-value')!.style.fontSize)
    expect(size(longer)).toBeLessThan(size(short))
    expect(size(longer)).toBeGreaterThanOrEqual(9)
  })

  it('shows a single part as a closed ring', () => {
    const el = mount(DonutChart, { ...props, slices: [slices[0]] })
    expect(all(el, 'path.slice')).toHaveLength(1)
    expect(el.querySelector('path.slice')!.getAttribute('d')!.match(/Z/g)).toHaveLength(2)
    expect(text(el.querySelector('.parts'))).toContain('100%')
  })

  it('goes round the parts with the arrow keys, and the centre says which', async () => {
    const el = mount(DonutChart, props)
    const ring = el.querySelector<HTMLElement>('.ring')!
    expect(ring.getAttribute('role')).toBe('group')
    ring.focus()
    await nextTick()
    expect(text(el.querySelector('.centre-value'))).toBe('600 T')
    expect(text(el.querySelector('.centre-label'))).toBe('Wallet')
    expect(live(el)).toBe('Wallet: 600 T, 60%')
    key(ring, 'ArrowRight')
    await nextTick()
    expect(live(el)).toBe('Gateway: 300 T, 30%')
    expect(all(el, '.parts li')[1].classList.contains('on')).toBe(true)
    key(ring, 'End')
    await nextTick()
    expect(live(el)).toContain('Manual')
    key(ring, 'Escape')
    await nextTick()
    expect(text(el.querySelector('.centre-value'))).toBe('1,000 T')
    // hovering a slice picks it too
    el.querySelector('path.slice')!.dispatchEvent(new MouseEvent('pointerenter'))
    await nextTick()
    expect(text(el.querySelector('.tip .title'))).toBe('Wallet')
  })

  it('turns the other way in Persian, so the parts follow the reading direction', () => {
    const en = mount(DonutChart, props, 'en')
    const fa = mount(DonutChart, props, 'fa')
    expect(en.querySelector('svg g[transform]')).toBeNull()
    expect(fa.querySelector('svg g[transform]')?.getAttribute('transform')).toContain('scale(-1,1)')
  })

  it('has a table, an empty state and a placeholder', async () => {
    const el = mount(DonutChart, props)
    el.querySelector<HTMLButtonElement>('.table-btn')!.click()
    await nextTick()
    expect(all(el, '.chart-table tbody tr').map((r) => all(r, 'th,td').map(text))).toEqual([
      ['Wallet', '600 T', '60%'],
      ['Gateway', '300 T', '30%'],
      ['Manual', '100 T', '10%'],
    ])
    expect(text(mount(DonutChart, { ...props, slices: [slices[3]], emptyText: 'No payments' }).querySelector('.empty'))).toBe('No payments')
    expect(mount(DonutChart, { ...props, slices: [], loading: true }).querySelector('.skeleton')).not.toBeNull()
  })
})

describe('Heatmap', () => {
  const rows = ['Mon', 'Tue', 'Wed']
  const cols = Array.from({ length: 24 }, (_, h) => `${String(h).padStart(2, '0')}:00`)
  const values = rows.map((_, r) => cols.map((_, c) => (r === 1 && c === 21 ? 8 : r === 0 && c === 9 ? 2 : 0)))
  const props = { title: 'When customers pay', rows, cols, colTicks: cols.map((_, h) => String(h)), values, format: (n: number) => `${n}`, valueLabel: 'Paid orders', corner: 'Day' }

  it('shades one cell per row and column on a one-hue scale, the highest being the darkest', () => {
    const el = mount(Heatmap, props)
    const cells = all(el, '.cells rect')
    expect(cells).toHaveLength(72)
    const levels = cells.map((c) => Number(c.getAttribute('data-level')))
    expect(Math.max(...levels)).toBe(6)
    expect(levels.filter((l) => l === 6)).toHaveLength(1)
    expect(levels.filter((l) => l === 0).length).toBe(70)
    expect(cells.find((c) => c.getAttribute('data-level') === '6')!.getAttribute('fill')).toBe('var(--chart-seq-6)')
    expect(all(el, '.row-label').map(text)).toEqual(rows)
    expect(all(el, '.col-tick').length).toBeLessThanOrEqual(24)
    expect(text(el.querySelector('.scale'))).toContain('Less')
    expect(text(el.querySelector('.scale'))).toContain('More')
  })

  it('names the cell under the pointer and moves from cell to cell with the keys', async () => {
    const el = mount(Heatmap, props)
    const plot = el.querySelector<HTMLElement>('.plot')!
    plot.focus()
    await nextTick()
    expect(live(el)).toBe('Mon, 00:00: Paid orders 0')
    key(plot, 'ArrowRight')
    key(plot, 'ArrowDown')
    await nextTick()
    expect(live(el)).toBe('Tue, 01:00: Paid orders 0')
    key(plot, 'End')
    key(plot, 'Home')
    key(plot, 'ArrowUp')
    key(plot, 'ArrowUp') // stops at the edge
    await nextTick()
    expect(live(el)).toBe('Mon, 00:00: Paid orders 0')
    // the pointer: the cell of the busiest hour
    const cell = all(el, '.cells rect').find((c) => c.getAttribute('data-level') === '6')!
    const svg = el.querySelector('.plot svg')!
    move(svg, Number(cell.getAttribute('x')) + 2, Number(cell.getAttribute('y')) + 2)
    await nextTick()
    expect(text(el.querySelector('.tip .title'))).toBe('Tue, 21:00')
    expect(words(el.querySelector('.tip .row')!)).toBe('8 Paid orders')
    expect(el.querySelector('.cells rect.on')).not.toBeNull()
    key(plot, 'Escape')
    await nextTick()
    expect(el.querySelector('.tip')).toBeNull()
  })

  it('runs the hours from the right in Persian and the arrows follow the screen', async () => {
    const en = mount(Heatmap, props, 'en')
    const fa = mount(Heatmap, props, 'fa')
    const x0 = (e: HTMLElement) => Number(all(e, '.cells rect')[0].getAttribute('x'))
    expect(x0(fa)).toBeGreaterThan(x0(en))
    const plot = fa.querySelector<HTMLElement>('.plot')!
    plot.focus()
    key(plot, 'ArrowLeft') // left on screen is later in Persian
    await nextTick()
    expect(live(fa)).toContain('01:00')
  })

  it('has a table of every hour, and an empty state when nothing was sold', async () => {
    const el = mount(Heatmap, props)
    el.querySelector<HTMLButtonElement>('.table-btn')!.click()
    await nextTick()
    expect(all(el, '.chart-table thead th')).toHaveLength(25)
    expect(all(el, '.chart-table tbody tr')).toHaveLength(3)
    const none = mount(Heatmap, { ...props, values: rows.map(() => cols.map(() => 0)), emptyText: 'No paid orders' })
    expect(text(none.querySelector('.empty'))).toBe('No paid orders')
  })
})

describe('Sparkline', () => {
  it('is one image that says its trend in words, with an end dot', () => {
    const el = mount(Sparkline, { values: [1, 4, 2, 8], label: 'Revenue: rising over the period' })
    const svg = el.querySelector('svg')!
    expect(svg.getAttribute('role')).toBe('img')
    expect(svg.getAttribute('aria-label')).toBe('Revenue: rising over the period')
    expect(el.querySelectorAll('path')).toHaveLength(2) // the wash and the line
    expect(el.querySelectorAll('circle')).toHaveLength(1)
    expect(el.querySelector('path[stroke]')!.getAttribute('stroke')).toBe('var(--chart-c1)')
  })

  it('draws bars, a flat series and an empty one without breaking', () => {
    const bars = mount(Sparkline, { values: [1, 0, 3], label: 'x', kind: 'bar', colorSlot: 7 })
    expect(bars.querySelectorAll('path')).toHaveLength(3)
    expect(bars.querySelector('path')!.getAttribute('fill')).toBe('var(--chart-c8)')
    const flat = mount(Sparkline, { values: [5, 5, 5], label: 'x' })
    expect(flat.querySelector('path[stroke]')!.getAttribute('d')).not.toContain('NaN')
    const empty = mount(Sparkline, { values: [], label: 'x' })
    expect(empty.querySelectorAll('path, circle')).toHaveLength(0)
    expect(empty.querySelector('line.base')).not.toBeNull()
    const one = mount(Sparkline, { values: [3], label: 'x' })
    expect(one.querySelectorAll('circle')).toHaveLength(1)
    expect(one.querySelectorAll('path')).toHaveLength(0)
  })

  it('mirrors in Persian', () => {
    const x = (e: HTMLElement) => Number(e.querySelector('circle')!.getAttribute('cx'))
    expect(x(mount(Sparkline, { values: [1, 2, 3], label: 'x' }, 'fa'))).toBeLessThan(x(mount(Sparkline, { values: [1, 2, 3], label: 'x' }, 'en')))
  })
})
