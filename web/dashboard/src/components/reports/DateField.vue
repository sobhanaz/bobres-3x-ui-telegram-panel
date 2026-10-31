<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Popover from 'primevue/popover'
import {
  addDays,
  DATE_MAX,
  DATE_MIN,
  digits,
  firstWeekday,
  formatDate,
  formatInput,
  inRange,
  isoOf,
  monthGrid,
  monthLength,
  monthName,
  parseInput,
  parseIso,
  partsOf,
  shiftMonth,
  todayIn,
  weekday,
  weekdayName,
  type Calendar,
} from '../../calendar'
import { rtl as isRtl, type Lang } from '../../format'

// A date typed or picked in a calendar, Jalali or Gregorian. The value going in
// and out is always the Gregorian ISO date the API takes ("2026-10-14", "" when
// the text is no date); the calendar only decides how it is written in the
// field, which month grid opens and what the text is read as. Changing the
// calendar writes the same day in the other one, so the day never moves.
const props = defineProps<{
  id: string
  modelValue: string
  calendar: Calendar
  invalid?: boolean
  /** today in the store's time zone (the day the grid marks); the browser's own day when missing */
  today?: string
  inputClass?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const clamp = (iso: string) => (iso < DATE_MIN ? DATE_MIN : iso > DATE_MAX ? DATE_MAX : iso)
const todayIso = computed(() => clamp(props.today && parseIso(props.today) ? props.today : todayIn()))
const example = computed(() => formatInput(todayIso.value, lang.value, props.calendar))

// ---- the text field
const shown = () => (props.modelValue ? formatInput(props.modelValue, lang.value, props.calendar) : '')
const text = ref(shown())
// The value changed from outside (a preset, "custom" starting from the dates on
// screen): write it. When it is what the text already says, leave the text as typed.
watch(
  () => props.modelValue,
  (v) => {
    if ((parseInput(text.value, props.calendar) ?? '') !== v) text.value = shown()
  },
)
// A new calendar or language writes the same day the new way.
watch([() => props.calendar, lang], () => {
  text.value = shown()
})
function onType(v: string | undefined) {
  text.value = v ?? ''
  emit('update:modelValue', parseInput(text.value, props.calendar) ?? '')
}
// Leaving the field (or Enter) tidies a date typed loosely: "1405/7/2" becomes "1405/07/02".
function commit() {
  const iso = parseInput(text.value, props.calendar)
  if (iso) text.value = formatInput(iso, lang.value, props.calendar)
}

// ---- the month grid
const pop = ref<InstanceType<typeof Popover> | null>(null)
const panel = ref<HTMLElement | null>(null)
const view = reactive({ y: 1, m: 1 })
const focusIso = ref('')

function show(iso: string) {
  const p = partsOf(iso, props.calendar)
  view.y = p.y
  view.m = p.m
  focusIso.value = iso
}
// The button that opened the grid: a pick closes the grid, and the focus goes back to it.
const trigger = ref<HTMLElement | null>(null)
function open(e: Event) {
  trigger.value = e.currentTarget as HTMLElement | null
  show(clamp(props.modelValue && parseIso(props.modelValue) ? props.modelValue : todayIso.value))
  pop.value?.toggle(e)
}
function focusDay(iso: string) {
  void nextTick(() => panel.value?.querySelector<HTMLElement>(`[data-date="${iso}"]`)?.focus())
}

const weeks = computed(() => monthGrid(props.calendar, view.y, view.m))
const heads = computed(() =>
  Array.from({ length: 7 }, (_, i) => {
    const wd = (firstWeekday(props.calendar) + i) % 7
    return { wd, narrow: weekdayName(wd, lang.value, 'narrow'), long: weekdayName(wd, lang.value, 'long') }
  }),
)
const title = computed(() => `${monthName(props.calendar, lang.value, view.m, view.y)} ${digits(String(view.y), lang.value)}`)
const dayNumber = (iso: string) => digits(String(partsOf(iso, props.calendar).d), lang.value)
const dayLabel = (iso: string) => `${weekdayName(weekday(iso), lang.value, 'long')} ${formatDate(iso, lang.value, props.calendar)}`

// A month the supported years do not reach is not offered.
function reachable(by: number): boolean {
  const n = shiftMonth(view, by)
  const first = isoOf(props.calendar, n.y, n.m, 1)
  const last = isoOf(props.calendar, n.y, n.m, monthLength(props.calendar, n.y, n.m))
  return !!first && !!last && last >= DATE_MIN && first <= DATE_MAX
}
// The same day of another month (the last one that month has, when it is shorter).
function monthStep(iso: string, by: number): string | null {
  const p = partsOf(iso, props.calendar)
  const n = shiftMonth(p, by)
  return isoOf(props.calendar, n.y, n.m, Math.min(p.d, monthLength(props.calendar, n.y, n.m)))
}
function moveTo(iso: string | null) {
  if (!iso || !inRange(iso)) return
  show(iso)
  focusDay(iso)
}
function go(by: number) {
  const target = monthStep(focusIso.value, by)
  if (reachable(by) && target) show(clamp(target))
}

function onKey(e: KeyboardEvent) {
  const cell = (e.target as HTMLElement).closest<HTMLElement>('[data-date]')
  const iso = cell?.dataset.date
  if (!iso) return
  const rtl = isRtl(lang.value) // the page reads right to left in Persian and Arabic
  const offset = (weekday(iso) - firstWeekday(props.calendar) + 7) % 7
  switch (e.key) {
    case 'ArrowLeft':
      moveTo(addDays(iso, rtl ? 1 : -1))
      break
    case 'ArrowRight':
      moveTo(addDays(iso, rtl ? -1 : 1))
      break
    case 'ArrowUp':
      moveTo(addDays(iso, -7))
      break
    case 'ArrowDown':
      moveTo(addDays(iso, 7))
      break
    case 'Home':
      moveTo(addDays(iso, -offset))
      break
    case 'End':
      moveTo(addDays(iso, 6 - offset))
      break
    case 'PageUp':
      moveTo(monthStep(iso, e.shiftKey ? -12 : -1))
      break
    case 'PageDown':
      moveTo(monthStep(iso, e.shiftKey ? 12 : 1))
      break
    default:
      return
  }
  e.preventDefault()
}

function pick(iso: string) {
  if (!inRange(iso)) return
  text.value = formatInput(iso, lang.value, props.calendar)
  emit('update:modelValue', iso)
  pop.value?.hide()
  void nextTick(() => trigger.value?.focus())
}
</script>

<template>
  <span class="date-input">
    <InputText
      :id="id"
      :model-value="text"
      :class="['date-text', inputClass]"
      :invalid="invalid"
      :placeholder="example"
      dir="ltr"
      inputmode="numeric"
      autocomplete="off"
      spellcheck="false"
      maxlength="24"
      @update:model-value="onType"
      @blur="commit"
      @keydown.enter="commit"
    />
    <Button
      type="button"
      icon="pi pi-calendar"
      text
      rounded
      size="small"
      class="open"
      :aria-label="t('reports.date.open')"
      aria-haspopup="dialog"
      @click="open"
    />
    <Popover ref="pop" @show="focusDay(focusIso)">
      <div ref="panel" class="picker" :data-calendar="calendar" @keydown="onKey">
        <div class="head">
          <Button type="button" text rounded size="small" class="nav prev-year" :disabled="!reachable(-12)" :aria-label="t('reports.date.prev_year')" @click="go(-12)">
            <i class="pi pi-angle-double-left app-flip" aria-hidden="true" />
          </Button>
          <Button type="button" text rounded size="small" class="nav prev-month" :disabled="!reachable(-1)" :aria-label="t('reports.date.prev_month')" @click="go(-1)">
            <i class="pi pi-angle-left app-flip" aria-hidden="true" />
          </Button>
          <strong class="title" aria-live="polite">{{ title }}</strong>
          <Button type="button" text rounded size="small" class="nav next-month" :disabled="!reachable(1)" :aria-label="t('reports.date.next_month')" @click="go(1)">
            <i class="pi pi-angle-right app-flip" aria-hidden="true" />
          </Button>
          <Button type="button" text rounded size="small" class="nav next-year" :disabled="!reachable(12)" :aria-label="t('reports.date.next_year')" @click="go(12)">
            <i class="pi pi-angle-double-right app-flip" aria-hidden="true" />
          </Button>
        </div>
        <table class="grid" role="grid" :aria-label="title">
          <thead>
            <tr>
              <th v-for="h in heads" :key="h.wd" scope="col" :abbr="h.long" :title="h.long">{{ h.narrow }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(week, i) in weeks" :key="i">
              <td v-for="(iso, j) in week" :key="j">
                <button
                  v-if="iso"
                  type="button"
                  class="day"
                  :class="{ selected: iso === modelValue, today: iso === todayIso }"
                  :data-date="iso"
                  :disabled="!inRange(iso)"
                  :tabindex="iso === focusIso ? 0 : -1"
                  :aria-label="dayLabel(iso)"
                  :aria-pressed="iso === modelValue"
                  :aria-current="iso === todayIso ? 'date' : undefined"
                  @click="pick(iso)"
                >
                  {{ dayNumber(iso) }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
        <div class="foot">
          <Button type="button" text size="small" class="pick-today" :label="t('reports.date.today')" @click="pick(todayIso)" />
        </div>
      </div>
    </Popover>
  </span>
</template>

<style scoped>
.date-input {
  display: inline-flex;
  gap: 0.15rem;
  align-items: center;
}
.date-text {
  inline-size: 9.5rem;
  text-align: center;
  font-variant-numeric: tabular-nums;
}
.picker {
  min-inline-size: 17.5rem;
}
.head {
  display: flex;
  gap: 0.15rem;
  align-items: center;
  margin-block-end: 0.4rem;
}
.title {
  flex: 1;
  text-align: center;
}
.grid {
  inline-size: 100%;
  border-collapse: collapse;
}
.grid th {
  padding-block: 0.25rem;
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--p-text-muted-color);
}
.grid td {
  padding: 1px;
  text-align: center;
}
.day {
  inline-size: 2.3rem;
  block-size: 2.3rem;
  font: inherit;
  color: inherit;
  cursor: pointer;
  background: transparent;
  border: 0;
  border-radius: 50%;
}
.day:hover:not(:disabled) {
  background: var(--p-content-hover-background);
}
.day:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 1px;
}
.day.today {
  box-shadow: inset 0 0 0 1px var(--p-primary-color);
}
.day.selected {
  color: var(--p-primary-contrast-color);
  background: var(--p-primary-color);
}
.day:disabled {
  cursor: default;
  opacity: 0.35;
}
.foot {
  display: flex;
  justify-content: center;
  margin-block-start: 0.4rem;
}
</style>
