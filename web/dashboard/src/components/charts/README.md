# Chart kit

Small SVG charts for the dashboard, with no chart library. Written for the Reports page and meant to be reused (the
System > Monitoring page is next). Everything is drawn from props; nothing here knows about sales.

```ts
import { LineChart, BarChart, DonutChart, Heatmap, Sparkline } from '../components/charts'
```

| Component | For | Notes |
|---|---|---|
| `LineChart` | a value over an ordered axis (time) | lines, areas, stacked areas, a dashed segment into a partial period |
| `BarChart` | the same, as columns (grouped or stacked) or a ranking (`orientation="horizontal"`) | bars are at most 28 px thick, rounded at the value end |
| `DonutChart` | the parts of a whole | the list beside it is the legend and the value labels |
| `Heatmap` | a value on a rows x columns grid (weekday x hour, host x time) | one hue, cells at most 28 px high |
| `Sparkline` | a word-sized trend in a card or a table row | one image with a text label |
| `ChartFrame` | what the four big ones sit in: title, legend, states, table button, live region | use it for a chart of your own |

3D versions live in `../charts3d` (`IsoColumns`, `TiltDonut`) and are meant to be loaded on demand
(`defineAsyncComponent`), never imported by a page directly: see "3D" below.

## Data

Words, numbers and dates are **given to the chart already formatted**, or as functions that format them: the kit never
formats a date or an amount itself, so the same chart takes Persian digits, the Jalali calendar, bytes or percent.

```ts
interface ChartSeries {
  key: string            // stable id: legend toggles and colours follow it, never the position
  label: string          // the series' name
  values: number[]       // one per category, in the order of `categories`
  slot?: number | 'other' // colour slot 0..7 (default: position); 'other' is the neutral grey
  dashed?: boolean       // a line is dashed: a second encoding besides colour
  area?: boolean         // a line chart fills under this series with a light wash
}
type ValueFormat = (n: number) => string
```

Common props of `LineChart`, `BarChart`:

| Prop | |
|---|---|
| `title`, `subtitle?` | the figure's caption; `title` is also the default accessible name |
| `categories: string[]` | the **full** name of each point (tooltip, table, screen reader): `"15 Oct 2026"` |
| `ticks?: string[]` | the short name for the axis (`"15 Oct"`); default: `categories` |
| `series: ChartSeries[]` | |
| `format: ValueFormat` | the value in a tooltip or table: `"1,200,000 Toman"` |
| `axisFormat?: ValueFormat` | the short form for the value axis: `"1.2M"`; default: `format` |
| `partial?: boolean[]` | points that hold only part of their period: dashed segment (line), lighter bar, a note in the tooltip |
| `height?: number` | px, default 240 |
| `rtl?: boolean` | default follows the language (Persian is right to left) |
| `loading?: boolean` | see states |
| `emptyText?: string` | shown instead of the chart when there is nothing |
| `corner?: string` | header of the table's first column (`"Period"`) |
| `summary?: string` | the chart's accessible name, ideally a sentence ("Revenue peaked on 4 Oct") |

`LineChart` also takes `stacked`, `yMin`/`yMax` (a fixed range, e.g. 0 to 100 for a percent), `baseline: 'zero' | 'auto'`
(start the value axis at zero, the default, or fit the data) and `markers` (a dot on every point; default: when there
are 24 or fewer). `BarChart` takes `mode: 'grouped' | 'stacked'`, `orientation` and `labels` (the value at the tip of
every bar: for a few bars). `DonutChart` takes `slices: { key, label, value, slot? }[]`, `percent(pct) => string`,
`centerFormat?` (the total in the hole, short), `centerLabel?` and `size`. `Heatmap` takes `rows`, `cols`, `colTicks?`,
`values[row][col]` and `valueLabel` (what the number is, for the tooltip). `Sparkline` takes `values`, `label` (say the
trend in words), `kind: 'line' | 'bar'`, `colorSlot?`, `width`, `height`.

```vue
<LineChart
  title="Bandwidth" :categories="stamps" :ticks="shortStamps"
  :series="[{ key: 'in', label: 'In', values: inBytes, slot: 0, area: true }, { key: 'out', label: 'Out', values: outBytes, slot: 1 }]"
  :format="bytes" :axis-format="bytesShort" corner="Time" :loading="refreshing"
/>
```

## Colour

Colours are CSS variables, never a hex in a component (`styles.css`): `--chart-c1` to `--chart-c8` (categorical),
`--chart-other`, `--chart-seq-0` to `--chart-seq-6` (one hue for magnitude), `--chart-grid`, `--chart-axis`,
`--chart-axis-text`, `--chart-surface`, `--chart-hover`. The dark theme and print (`@media print`: always light) have
their own values. The eight slots are a palette checked for colour-blind readers (adjacent slots stay apart), so:

- **Use slots in order and never cycle.** A ninth thing is "other" (`slot: 'other'`).
- **Colour follows the thing, not its rank.** Pass the same `slot` for the same thing on every chart (a plan, a
  server, a status), so hiding one series never repaints the others.
- Three light-theme slots (aqua, yellow, magenta) are under 3:1 against white: that is why every chart has a legend,
  a tooltip and a table, and why text never wears a series colour.
- Status colours (good, warning, critical) are not slots: show them with an icon and a word.

## Interaction

- **Pointer:** a crosshair (lines) or a lit band (bars) snaps to the nearest point; one tooltip lists **every series**
  at it. Touch works the same (a tap picks).
- **Keyboard:** the chart is a focus stop. Arrows move along the axis **the way they point on screen** (the right
  arrow is the earlier point in Persian), Home/End jump, Escape leaves. A 2D heatmap uses all four arrows.
- **Legend:** one button per series (`aria-pressed`) shows or hides it; the last visible series stays. One series has
  no legend (its title names it).
- Tooltips are text (`textContent`), never HTML: names from the data are safe to pass.

## Accessibility

- The plot is `role="group"` with `aria-roledescription="chart"`, an accessible name (`summary` or `title`) and a
  description that says which keys work; the drawing itself is `aria-hidden`.
- The focused point is also written into a polite live region ("4 Oct 2026: Revenue 400 T, Kept 380 T").
- **Every chart has a table** behind a "Show as table" button: the same numbers, with a sticky header and first
  column, scrollable by keyboard. Nothing is reachable by hover alone.
- Colour is never the only key: legend names, dashes for a second line, the list beside a donut.
- `prefers-reduced-motion`: the entrance animations are CSS and the app's stylesheet switches them off; the 3D
  charts' own entrance (a JS tween) is skipped too.
- Contrast: axis text uses the muted text colour, values the normal one.

## States

- `loading` with nothing to show yet: a placeholder of the chart's height (`role="status"`).
- `loading` with data already drawn (a refetch): the **old drawing stays**, dimmed, with `aria-busy`: no jump, no flash.
- No data (`categories` or `series` empty, a donut with no positive part, a heatmap of zeros): `emptyText`, or a
  default "Nothing to show for this period." in the dashboard's language.

## Right to left

The drawing is laid out in physical coordinates and mirrored by the chart: in Persian the oldest point is at the
**right**, the value axis is on the right, bars of a ranking grow from the right and the donut turns anticlockwise.
Text is placed with explicit anchors, so a Persian label is never flipped. Force a direction with `rtl`.

## Writing a chart of your own

`ChartFrame` gives the figure, caption, legend (`:legend` items, `@toggle`), states, table (`:table`) and live region
(`:live`); put the drawing in its default slot and use `scale.ts` (ticks, scales, stacks, bar/line/area/arc paths),
`palette.ts` and `useChart.ts` (`useChartWidth`, `useRtl`, `useReducedMotion`, `keyStep`, `stepIndex`). `scale.ts` is
pure and unit tested (`__tests__/charts-scale.test.ts`).

## 3D (`../charts3d`)

`IsoColumns` (stacked columns in 3D) and `TiltDonut` (a thick, tilting donut) take the same props as `BarChart`
(`mode` stacked) and `DonutChart`, plus `still` (no entrance motion). They are SVG polygons from an orthographic camera
(`projection.ts`): no WebGL and no library, so they scale, print and need no fallback for a missing GPU. Turn them with
a drag or the two sliders. They are a **second view of the same numbers**, never the only one:

- load them with `defineAsyncComponent(() => import('../charts3d/IsoColumns.vue'))` so they stay out of the page's
  bundle, and only when they are shown;
- show the flat chart instead on a phone (under 640 px), when the person prefers reduced motion (unless they switch
  to 3D) and for more than about 60 columns: the Professional report does exactly this (`ProfessionalReport.vue`).

## Tests

`__tests__/charts-scale.test.ts` (geometry), `charts.test.ts` (the five charts mounted), `charts3d.test.ts` (camera,
the two 3D charts). A new chart should come with the same kind of test: what it draws, a pointer, the keys, the empty
and loading states, the table, Persian.
