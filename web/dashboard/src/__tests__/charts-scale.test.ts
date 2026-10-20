import { describe, expect, it } from 'vitest'
import {
  areaPath,
  arcPath,
  bandIndex,
  bandOf,
  barPath,
  level,
  linear,
  linePath,
  nearestPoint,
  niceStep,
  niceTicks,
  pointX,
  polar,
  sliceAngles,
  stackBounds,
  textWidth,
  tickIndexes,
  truncate,
} from '../components/charts/scale'
import { keyStep, stepIndex } from '../components/charts/useChart'
import { seriesColor, sequentialColor } from '../components/charts/palette'

// The geometry of the chart kit: every number a chart draws comes from here, so it is checked without a browser.

describe('ticks and scales', () => {
  it('rounds a step to 1, 2, 2.5, 5 or 10 times a power of ten', () => {
    expect(niceStep(0.7)).toBe(1)
    expect(niceStep(1.3)).toBe(2)
    expect(niceStep(2.2)).toBe(2.5)
    expect(niceStep(3.9)).toBe(5)
    expect(niceStep(7)).toBe(10)
    expect(niceStep(430)).toBe(500)
    expect(niceStep(0)).toBe(1)
    expect(niceStep(NaN)).toBe(1)
  })

  it('gives round ticks that cover the data and start at zero for bars and areas', () => {
    expect(niceTicks(0, 9_200_000)).toEqual({ ticks: [0, 2_500_000, 5_000_000, 7_500_000, 10_000_000], min: 0, max: 10_000_000 })
    // a series that never goes near zero still starts there unless the chart asks for a fitted axis
    expect(niceTicks(60, 94).min).toBe(0)
    const fitted = niceTicks(60, 94, 4, false)
    expect(fitted.min).toBeGreaterThan(0)
    expect(fitted.max).toBeGreaterThanOrEqual(94)
    expect(fitted.ticks.every((t, i, a) => i === 0 || t > a[i - 1])).toBe(true)
  })

  it('handles negative values, nothing at all and a single value', () => {
    const neg = niceTicks(-300, 1200)
    expect(neg.min).toBeLessThanOrEqual(-300)
    expect(neg.ticks).toContain(0)
    expect(niceTicks(0, 0)).toEqual({ ticks: [0, 1, 2, 3, 4], min: 0, max: 4 }) // whole steps, not quarters
    expect(niceTicks(NaN, NaN).ticks.length).toBeGreaterThan(1)
    // no floating point dust in the ticks
    expect(niceTicks(0, 0.7).ticks).toEqual([0, 0.2, 0.4, 0.6, 0.8])
  })

  it('maps a domain onto a range, and a flat domain to the middle', () => {
    const f = linear(0, 100, 200, 0)
    expect([f(0), f(50), f(100)]).toEqual([200, 100, 0])
    expect(linear(5, 5, 0, 10)(5)).toBe(5)
  })

  it('spreads points and bands and finds the one nearest a pointer', () => {
    expect([0, 1, 2].map((i) => pointX(i, 3, 10, 30))).toEqual([10, 20, 30])
    expect(pointX(0, 1, 10, 30)).toBe(20)
    expect(nearestPoint(14, 3, 10, 30)).toBe(0)
    expect(nearestPoint(16, 3, 10, 30)).toBe(1)
    expect(nearestPoint(-50, 3, 10, 30)).toBe(0)
    expect(nearestPoint(500, 3, 10, 30)).toBe(2)
    expect(nearestPoint(5, 1, 0, 10)).toBe(0)
    expect(bandOf(2, 4, 0, 100)).toEqual({ start: 50, size: 25 })
    expect([0, 24, 25, 99, 100, 130, -5].map((x) => bandIndex(x, 4, 0, 100))).toEqual([0, 0, 1, 3, 3, 3, 0])
  })

  it('thins the labels of a long axis to what fits, always keeping the first', () => {
    expect(tickIndexes(5, 10)).toEqual([0, 1, 2, 3, 4])
    expect(tickIndexes(130, 6)[0]).toBe(0)
    expect(tickIndexes(130, 6).length).toBeLessThanOrEqual(6)
    expect(tickIndexes(0, 6)).toEqual([])
  })
})

describe('stacks and paths', () => {
  it('stacks series on each other and draws a negative part as nothing', () => {
    const s = stackBounds([[1, 2], [3, -4], [5, 6]])
    expect(s.lower).toEqual([[0, 0], [1, 2], [4, 2]])
    expect(s.upper).toEqual([[1, 2], [4, 2], [9, 8]])
    expect(stackBounds([])).toEqual({ lower: [], upper: [] })
  })

  it('joins points into a line and fills between a line and a baseline', () => {
    expect(linePath([{ x: 0, y: 10 }, { x: 5, y: 2.5 }])).toBe('M0,10L5,2.5')
    expect(linePath([])).toBe('')
    const area = areaPath([{ x: 0, y: 4 }, { x: 10, y: 2 }], [{ x: 0, y: 10 }, { x: 10, y: 10 }])
    expect(area).toBe('M0,4L10,2L10,10L0,10Z')
    expect(areaPath([], [])).toBe('')
  })

  it('rounds only the end of a bar that carries the value', () => {
    const top = barPath(0, 0, 20, 50, 4, 'top')
    expect(top).toContain('A4,4')
    expect(top.startsWith('M0,50V4')).toBe(true) // the base corners are square: it starts at the bottom left and goes straight up
    expect(top.endsWith('V50Z')).toBe(true)
    expect(barPath(0, 0, 20, 50, 4, 'none')).toBe('M0,0H20V50H0Z')
    expect(barPath(0, 0, 20, 50, 4, 'right')).toContain('A4,4')
    expect(barPath(0, 0, 20, 50, 4, 'left')).toContain('A4,4')
    expect(barPath(0, 0, 20, 50, 4, 'bottom')).toContain('A4,4')
    // a bar thinner than its radius rounds by what it has, and an empty one is nothing
    expect(barPath(0, 0, 4, 50, 4, 'top')).toContain('A2,2')
    expect(barPath(0, 0, 0, 50, 4, 'top')).toBe('')
    expect(barPath(0, 0, 20, 0, 4, 'top')).toBe('')
  })

  it('draws donut slices, a whole turn as a closed ring, and nothing for an empty slice', () => {
    expect(polar(0, 0, 10, 0)).toEqual({ x: 0, y: -10 })
    const half = polar(0, 0, 10, Math.PI / 2)
    expect(half.x).toBeCloseTo(10)
    expect(half.y).toBeCloseTo(0)
    expect(arcPath(50, 50, 20, 40, 0, Math.PI)).toMatch(/^M.*A40,40 0 0 1 .*A20,20 0 0 0 .*Z$/)
    expect(arcPath(50, 50, 20, 40, 0, Math.PI * 1.5)).toContain('A40,40 0 1 1') // more than half a turn takes the long way
    const ring = arcPath(50, 50, 20, 40, 0, Math.PI * 2)
    expect(ring.match(/Z/g)).toHaveLength(2) // an outer and an inner circle
    expect(arcPath(50, 50, 20, 40, 1, 1)).toBe('')
  })

  it('turns values into angles that fill a turn, giving no angle to what is not positive', () => {
    const a = sliceAngles([1, 1, 2])
    expect(a[0]).toEqual([0, Math.PI / 2])
    expect(a[2][1]).toBeCloseTo(Math.PI * 2)
    expect(sliceAngles([0, 5, -2]).map(([s, e]) => e - s)).toEqual([0, Math.PI * 2, 0])
    expect(sliceAngles([0, 0]).every(([s, e]) => s === 0 && e === 0)).toBe(true)
  })
})

describe('colours, levels and text', () => {
  it('puts a value on a one-hue scale: 0 for nothing, 1 to 6 up to the maximum', () => {
    expect(level(0, 10)).toBe(0)
    expect(level(-1, 10)).toBe(0)
    expect(level(5, 0)).toBe(0)
    expect(level(0.01, 10)).toBe(1) // anything above nothing shows
    expect(level(10, 10)).toBe(6)
    expect(level(5, 10)).toBe(3)
  })

  it('names the colour of a slot, and the neutral one past the eighth', () => {
    expect(seriesColor(0)).toBe('var(--chart-c1)')
    expect(seriesColor(undefined, 2)).toBe('var(--chart-c3)')
    expect(seriesColor(7)).toBe('var(--chart-c8)')
    expect(seriesColor(8)).toBe('var(--chart-other)')
    expect(seriesColor('other')).toBe('var(--chart-other)')
    expect(seriesColor(-1)).toBe('var(--chart-other)')
    expect(sequentialColor(0)).toBe('var(--chart-seq-0)')
    expect(sequentialColor(99)).toBe('var(--chart-seq-6)')
  })

  it('estimates a label width and shortens a long name with an ellipsis', () => {
    expect(textWidth('')).toBe(0)
    expect(textWidth('1,000')).toBeGreaterThan(textWidth('1'))
    expect(truncate('short', 10)).toBe('short')
    expect(truncate('a very long plan name', 8)).toBe('a very …')
    expect([...truncate('پلن بسیار طولانی با نام دراز', 8)]).toHaveLength(8)
  })
})

describe('the keys of a chart', () => {
  const key = (k: string) => new KeyboardEvent('keydown', { key: k })
  it('moves the way the arrow points on screen, so the right arrow is earlier in right-to-left', () => {
    expect(keyStep(key('ArrowRight'), false)).toBe('next')
    expect(keyStep(key('ArrowLeft'), false)).toBe('prev')
    expect(keyStep(key('ArrowRight'), true)).toBe('prev')
    expect(keyStep(key('ArrowLeft'), true)).toBe('next')
    expect(keyStep(key('ArrowDown'), true)).toBe('next')
    expect(keyStep(key('ArrowUp'), false)).toBe('prev')
    expect(keyStep(key('Home'), false)).toBe('first')
    expect(keyStep(key('End'), false)).toBe('last')
    expect(keyStep(key('Escape'), false)).toBe('clear')
    expect(keyStep(key('a'), false)).toBeNull()
  })

  it('steps an index within the points, and starts from the near end', () => {
    expect(stepIndex(null, 'next', 5)).toBe(0)
    expect(stepIndex(null, 'prev', 5)).toBe(4)
    expect(stepIndex(2, 'next', 5)).toBe(3)
    expect(stepIndex(4, 'next', 5)).toBe(4)
    expect(stepIndex(0, 'prev', 5)).toBe(0)
    expect(stepIndex(2, 'first', 5)).toBe(0)
    expect(stepIndex(2, 'last', 5)).toBe(4)
    expect(stepIndex(2, 'clear', 5)).toBeNull()
    expect(stepIndex(2, null, 5)).toBe(2)
    expect(stepIndex(null, 'next', 0)).toBeNull()
  })
})
