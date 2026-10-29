// The geometry of the daily wheel. Pure functions, no Vue: the screen draws what these say, and the tests check them. The app never decides where
// the wheel stops: core draws on the server, and the screen turns the wheel to the segment core named.
import type { WheelPrize } from './api'

/** A piece of the wheel. Angles are degrees clockwise from the top, where the pointer is; the pieces cover 0 to 360. */
export interface Segment {
  /** the place of the prize in the list; -1 for the piece that pays nothing */
  index: number
  amount: number
  weight: number
  start: number
  end: number
}

/** segmentsOf lays the prizes round the wheel in the order core listed them, the piece that pays nothing last, each as wide as its weight. */
export function segmentsOf(prizes: readonly WheelPrize[], noPrizeWeight: number): Segment[] {
  const total = prizes.reduce((sum, p) => sum + p.weight, 0) + Math.max(noPrizeWeight, 0)
  if (total <= 0) return []
  const out: Segment[] = []
  let at = 0
  const add = (index: number, amount: number, weight: number) => {
    const end = at + (weight / total) * 360
    out.push({ index, amount, weight, start: at, end })
    at = end
  }
  prizes.forEach((p, i) => add(i, p.amount, p.weight))
  if (noPrizeWeight > 0) add(-1, 0, noPrizeWeight)
  out[out.length - 1].end = 360 // no rounding gap at the top
  return out
}

/** segmentFor finds the piece core's answer names: the prize at index, or the piece that pays nothing for a spin that won none (index -1). */
export function segmentFor(segments: readonly Segment[], index: number): Segment | undefined {
  return segments.find((s) => s.index === index)
}

/**
 * targetRotation is the rotation (degrees, clockwise) that brings the angle `at` of the wheel under the pointer, at least `turns` full turns after
 * `current`, so the wheel always spins forward. It ends with (at + rotation) a whole number of turns.
 */
export function targetRotation(current: number, at: number, turns = 5): number {
  const rest = (((at + current) % 360) + 360) % 360
  return current + turns * 360 + ((360 - rest) % 360)
}

/** restAngle is where in a piece the wheel stops: `frac` of the way across it, kept off its edges so the pointer never touches a border. */
export function restAngle(seg: Segment, frac: number): number {
  const f = Math.min(Math.max(frac, 0), 1)
  return seg.start + (seg.end - seg.start) * (0.15 + 0.7 * f)
}

const rad = (deg: number) => (deg * Math.PI) / 180

/** point is the place on a circle at an angle clockwise from the top. */
export function point(cx: number, cy: number, r: number, deg: number): [number, number] {
  return [cx + r * Math.sin(rad(deg)), cy - r * Math.cos(rad(deg))]
}

const fixed = (n: number) => String(Math.round(n * 100) / 100)

/** sectorPath draws one piece as an SVG path from the centre. (A piece that is the whole wheel is a circle, which the caller draws.) */
export function sectorPath(cx: number, cy: number, r: number, start: number, end: number): string {
  const [x1, y1] = point(cx, cy, r, start)
  const [x2, y2] = point(cx, cy, r, end)
  const large = end - start > 180 ? 1 : 0
  return `M${fixed(cx)} ${fixed(cy)} L${fixed(x1)} ${fixed(y1)} A${fixed(r)} ${fixed(r)} 0 ${large} 1 ${fixed(x2)} ${fixed(y2)} Z`
}

/** chanceOf is a piece's chance as a fraction of 1. */
export function chanceOf(weight: number, total: number): number {
  return total > 0 ? weight / total : 0
}
