// Pure geometry for the charts: scales, ticks, stacks and the path of a bar, a
// line, an area and a donut slice. No Vue and no DOM here, so every function is
// checked by plain unit tests.

/** niceStep rounds a raw step up to 1, 2, 2.5, 5 or 10 times a power of ten. */
export function niceStep(raw: number): number {
  if (!(raw > 0) || !Number.isFinite(raw)) return 1
  const exp = Math.floor(Math.log10(raw))
  const f = raw / 10 ** exp
  const nf = f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10
  return nf * 10 ** exp
}

export interface Ticks {
  ticks: number[]
  min: number
  max: number
}

/** niceTicks gives round tick values that cover [min, max], about `count` of
 * them. With zero set (the default) the scale always includes 0, as bars and
 * areas need. */
export function niceTicks(min: number, max: number, count = 4, zero = true): Ticks {
  let lo = Number.isFinite(min) ? min : 0
  let hi = Number.isFinite(max) ? max : 1
  if (zero) {
    lo = Math.min(lo, 0)
    hi = Math.max(hi, 0)
  }
  if (lo === hi) hi = lo + count // nothing to scale: a few whole steps, never 0.25
  const step = niceStep((hi - lo) / Math.max(1, count))
  const start = Math.floor(lo / step + 1e-9) * step
  const end = Math.ceil(hi / step - 1e-9) * step
  const ticks: number[] = []
  for (let v = start, i = 0; v <= end + step / 1e6 && i < 100; i++, v = start + i * step) ticks.push(Number(v.toFixed(10)))
  return { ticks, min: ticks[0], max: ticks[ticks.length - 1] }
}

/** linear maps [d0, d1] onto [r0, r1]; a flat domain maps to the middle. */
export function linear(d0: number, d1: number, r0: number, r1: number): (v: number) => number {
  if (d0 === d1) return () => (r0 + r1) / 2
  const k = (r1 - r0) / (d1 - d0)
  return (v) => r0 + (v - d0) * k
}

/** pointX is the x of point i of n spread from x0 to x1 (one point sits in the middle). */
export function pointX(i: number, n: number, x0: number, x1: number): number {
  return n <= 1 ? (x0 + x1) / 2 : x0 + (i * (x1 - x0)) / (n - 1)
}

/** nearestPoint is the index of the point of n spread from x0 to x1 closest to x. */
export function nearestPoint(x: number, n: number, x0: number, x1: number): number {
  if (n <= 1) return 0
  const i = Math.round(((x - x0) / (x1 - x0)) * (n - 1))
  return Math.min(n - 1, Math.max(0, i))
}

/** bandOf is the band (an equal slice of [x0, x1]) i of n, and the band x falls in. */
export function bandOf(i: number, n: number, x0: number, x1: number): { start: number; size: number } {
  const size = (x1 - x0) / Math.max(1, n)
  return { start: x0 + i * size, size }
}

export function bandIndex(x: number, n: number, x0: number, x1: number): number {
  const size = (x1 - x0) / Math.max(1, n)
  return Math.min(n - 1, Math.max(0, Math.floor((x - x0) / size)))
}

/** tickIndexes picks at most `max` evenly spaced labels out of n, always the first. */
export function tickIndexes(n: number, max: number): number[] {
  if (n <= 0) return []
  const step = Math.max(1, Math.ceil(n / Math.max(1, max)))
  const out: number[] = []
  for (let i = 0; i < n; i += step) out.push(i)
  return out
}

/** stackBounds stacks series on top of each other, category by category: the
 * lower and upper edge of each series' slice. A negative value is drawn as 0
 * (a stack of parts of a whole has no negative parts). */
export function stackBounds(values: number[][]): { lower: number[][]; upper: number[][] } {
  const n = values.reduce((m, v) => Math.max(m, v.length), 0)
  const run = new Array<number>(n).fill(0)
  const lower: number[][] = []
  const upper: number[][] = []
  for (const series of values) {
    const lo: number[] = []
    const hi: number[] = []
    for (let i = 0; i < n; i++) {
      const v = Math.max(0, series[i] ?? 0)
      lo.push(run[i])
      run[i] += v
      hi.push(run[i])
    }
    lower.push(lo)
    upper.push(hi)
  }
  return { lower, upper }
}

const f = (n: number) => Number(n.toFixed(2))

export interface XY {
  x: number
  y: number
}

/** linePath joins points with straight segments. */
export function linePath(points: XY[]): string {
  return points.map((p, i) => `${i === 0 ? 'M' : 'L'}${f(p.x)},${f(p.y)}`).join('')
}

/** areaPath fills between a line and a baseline (or a lower line, for a stack). */
export function areaPath(top: XY[], bottom: XY[]): string {
  if (!top.length) return ''
  const back = [...bottom].reverse()
  return linePath(top) + back.map((p) => `L${f(p.x)},${f(p.y)}`).join('') + 'Z'
}

export type Round = 'top' | 'bottom' | 'left' | 'right' | 'none'

/** barPath is a rectangle whose end that carries the data (top for a column,
 * right for a bar) is rounded by r and whose base is square. */
export function barPath(x: number, y: number, w: number, h: number, r: number, round: Round): string {
  if (!(w > 0) || !(h > 0)) return ''
  const rr = round === 'none' ? 0 : Math.max(0, Math.min(r, w / 2, h / 2))
  const x2 = x + w
  const y2 = y + h
  if (rr === 0) return `M${f(x)},${f(y)}H${f(x2)}V${f(y2)}H${f(x)}Z`
  switch (round) {
    case 'top':
      return `M${f(x)},${f(y2)}V${f(y + rr)}A${f(rr)},${f(rr)} 0 0 1 ${f(x + rr)},${f(y)}H${f(x2 - rr)}A${f(rr)},${f(rr)} 0 0 1 ${f(x2)},${f(y + rr)}V${f(y2)}Z`
    case 'bottom':
      return `M${f(x)},${f(y)}H${f(x2)}V${f(y2 - rr)}A${f(rr)},${f(rr)} 0 0 1 ${f(x2 - rr)},${f(y2)}H${f(x + rr)}A${f(rr)},${f(rr)} 0 0 1 ${f(x)},${f(y2 - rr)}Z`
    case 'right':
      return `M${f(x)},${f(y)}H${f(x2 - rr)}A${f(rr)},${f(rr)} 0 0 1 ${f(x2)},${f(y + rr)}V${f(y2 - rr)}A${f(rr)},${f(rr)} 0 0 1 ${f(x2 - rr)},${f(y2)}H${f(x)}Z`
    default:
      return `M${f(x2)},${f(y)}H${f(x + rr)}A${f(rr)},${f(rr)} 0 0 0 ${f(x)},${f(y + rr)}V${f(y2 - rr)}A${f(rr)},${f(rr)} 0 0 0 ${f(x + rr)},${f(y2)}H${f(x2)}Z`
  }
}

const TAU = Math.PI * 2

/** polar is the point at angle a (radians, 0 at twelve o'clock, clockwise) on a circle. */
export function polar(cx: number, cy: number, r: number, a: number): XY {
  return { x: cx + r * Math.sin(a), y: cy - r * Math.cos(a) }
}

/** arcPath is a donut slice from angle a0 to a1 between the radii r0 and r1. A
 * whole turn is drawn as a ring of two halves, since one arc cannot close on itself. */
export function arcPath(cx: number, cy: number, r0: number, r1: number, a0: number, a1: number): string {
  const span = Math.min(a1 - a0, TAU)
  if (!(span > 0)) return ''
  if (span >= TAU - 1e-6) {
    const top = polar(cx, cy, r1, 0)
    const bot = polar(cx, cy, r1, Math.PI)
    const itop = polar(cx, cy, r0, 0)
    const ibot = polar(cx, cy, r0, Math.PI)
    return (
      `M${f(top.x)},${f(top.y)}A${f(r1)},${f(r1)} 0 1 1 ${f(bot.x)},${f(bot.y)}A${f(r1)},${f(r1)} 0 1 1 ${f(top.x)},${f(top.y)}Z` +
      `M${f(itop.x)},${f(itop.y)}A${f(r0)},${f(r0)} 0 1 0 ${f(ibot.x)},${f(ibot.y)}A${f(r0)},${f(r0)} 0 1 0 ${f(itop.x)},${f(itop.y)}Z`
    )
  }
  const large = span > Math.PI ? 1 : 0
  const o0 = polar(cx, cy, r1, a0)
  const o1 = polar(cx, cy, r1, a0 + span)
  const i1 = polar(cx, cy, r0, a0 + span)
  const i0 = polar(cx, cy, r0, a0)
  return `M${f(o0.x)},${f(o0.y)}A${f(r1)},${f(r1)} 0 ${large} 1 ${f(o1.x)},${f(o1.y)}L${f(i1.x)},${f(i1.y)}A${f(r0)},${f(r0)} 0 ${large} 0 ${f(i0.x)},${f(i0.y)}Z`
}

/** sliceAngles turns values into [start, end] angles of a full turn; values
 * that are not positive take no angle. */
export function sliceAngles(values: number[]): [number, number][] {
  const total = values.reduce((s, v) => s + Math.max(0, v), 0)
  let a = 0
  return values.map((v) => {
    const span = total > 0 ? (Math.max(0, v) / total) * TAU : 0
    const out: [number, number] = [a, a + span]
    a += span
    return out
  })
}

/** level puts a value on a sequential scale of `levels` steps: 0 for nothing,
 * then 1 to levels - 1 up to the maximum. */
export function level(value: number, max: number, levels = 7): number {
  if (!(value > 0) || !(max > 0)) return 0
  return Math.min(levels - 1, Math.max(1, Math.ceil((value / max) * (levels - 1))))
}

/** textWidth estimates the width of a label in px, for margins: the real text
 * is measured by the browser, this only has to be about right. */
export function textWidth(s: string, size = 12): number {
  let w = 0
  for (const ch of s) w += /[؀-ۿ]/.test(ch) ? size * 0.55 : /[0-9.,]/.test(ch) ? size * 0.58 : size * 0.6
  return Math.ceil(w)
}

/** truncate shortens a label to about max characters with an ellipsis. */
export function truncate(s: string, max: number): string {
  const chars = [...s]
  return chars.length <= max ? s : chars.slice(0, Math.max(1, max - 1)).join('') + '…'
}
