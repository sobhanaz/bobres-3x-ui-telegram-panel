// The shapes the chart kit takes (components/charts/README.md). The kit knows
// nothing about sales: numbers arrive already as numbers, and words, dates and
// money arrive already formatted (or as the format functions below), so the
// same charts serve the Reports page and the monitoring page.

/** One series: a line, a set of bars or an area. `values` has one number per
 * category, in the order of the chart's `categories`. */
export interface ChartSeries {
  /** a stable id: legend toggles and colours follow it, never the position */
  key: string
  label: string
  values: number[]
  /** categorical colour slot 0 to 7 (default: the series' position). Pass the
   * same slot for the same thing on every chart, so a filter that removes a
   * series never repaints the others. 'other' is the neutral grey. */
  slot?: number | 'other'
  /** a line is drawn dashed (a second encoding besides colour) */
  dashed?: boolean
  /** a line chart fills under this series with a light wash */
  area?: boolean
}

/** A series of a line chart, which may have gaps: a `null` value means nothing was measured at that point (a
 * host that was off, a collector that was quiet). The line breaks there; it is never drawn as zero. */
export interface GapSeries extends Omit<ChartSeries, 'values'> {
  values: (number | null)[]
}

/** A number written for people: "1,200,000 Toman", "۱٫۲ میلیون", "42%". */
export type ValueFormat = (n: number) => string

/** One row of a tooltip. */
export interface TooltipRow {
  key: string
  label: string
  value: string
  color: string
  dashed?: boolean
}

export interface TooltipData {
  title: string
  rows: TooltipRow[]
  note?: string
}

/** What a chart tells its frame about its data, for the table alternative. */
export interface ChartTableData {
  /** header of the first column (the category: "Period", "Plan", "Hour") */
  corner: string
  columns: string[]
  rows: { label: string; cells: string[] }[]
}

export interface LegendItem {
  key: string
  label: string
  color: string
  dashed?: boolean
  /** a line key (stroke) instead of a swatch (fill) */
  line?: boolean
  hidden?: boolean
  /** text shown after the label (a share, a total) */
  detail?: string
}

/** One part of a donut. */
export interface DonutSlice {
  key: string
  label: string
  value: number
  slot?: number | 'other'
}
