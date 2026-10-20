// Colours of the charts are CSS variables (styles.css), so light, dark and print
// each have their own values and no chart holds a hex. The eight categorical
// slots are the data-viz skill's validated palette (blue, orange, aqua, yellow,
// magenta, green, violet, red); adjacent slots stay apart for colour-blind
// readers, so slots are used in this order and never cycled: a ninth thing is
// "other", the neutral grey.

export const SLOTS = 8

/** seriesColor is the CSS colour of a slot (or of a series at position i). */
export function seriesColor(slot: number | 'other' | undefined, i = 0): string {
  if (slot === 'other') return 'var(--chart-other)'
  const n = slot ?? i
  if (!Number.isInteger(n) || n < 0 || n >= SLOTS) return 'var(--chart-other)'
  return `var(--chart-c${n + 1})`
}

/** sequentialColor is the colour of a level 0 to 6 of the one-hue magnitude scale. */
export function sequentialColor(level: number): string {
  return `var(--chart-seq-${Math.min(6, Math.max(0, Math.round(level)))})`
}
