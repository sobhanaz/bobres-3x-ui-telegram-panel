<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import { asciiNumber, money, num, type Lang } from '../../format'
import { chancePercent, MAX_PRIZES, normalizePrizes, parsePrizes } from '../../settings'

// The prizes of the daily wheel (setting wheel.prizes): rows of "Toman won" and "weight", with each row's chance under it. The setting is compact
// JSON; the rows keep what was typed, so a half-typed number is not lost, and the value sent up is the rows as JSON (a row that is not a number is
// sent as text, which the card then flags). Saved sorted by amount. The chance is the weight out of the sum of every weight and the weight of "no
// prize" (a setting of its own, read from the card's draft).
const props = defineProps<{ modelValue: string; inputId: string; noPrizeWeight?: string; invalid?: boolean; locked?: boolean; describedBy?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; touch: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

interface Row {
  amount: string
  weight: string
}

function rowsOf(text: string): Row[] {
  const p = parsePrizes(text)
  return 'prizes' in p ? p.prizes.map((x) => ({ amount: String(x.amount), weight: String(x.weight) })) : []
}

const asNumber = (s: string): number | string => {
  const v = asciiNumber(s.trim())
  return /^\d{1,15}$/.test(v) ? Number(v) : s.trim()
}

/** draft is the rows as the value of the setting ("" for none). */
function draft(rs: Row[]): string {
  return rs.length ? JSON.stringify(rs.map((r) => ({ amount: asNumber(r.amount), weight: asNumber(r.weight) }))) : ''
}

const rows = ref<Row[]>(rowsOf(props.modelValue))
// A value that comes down from the card (a save, a reset) replaces the rows; the echo of our own edit does not.
watch(
  () => props.modelValue,
  (v) => {
    if (normalizePrizes(v) !== normalizePrizes(draft(rows.value))) rows.value = rowsOf(v)
  },
)

function changed() {
  emit('update:modelValue', draft(rows.value))
}
function setField(i: number, field: keyof Row, v: string | undefined) {
  rows.value = rows.value.map((r, j) => (j === i ? { ...r, [field]: v ?? '' } : r))
  changed()
}
function add() {
  rows.value = [...rows.value, { amount: '', weight: '' }]
  changed()
  emit('touch')
}
function remove(i: number) {
  rows.value = rows.value.filter((_, j) => j !== i)
  changed()
  emit('touch')
}

const canAdd = computed(() => !props.locked && rows.value.length < MAX_PRIZES)
// The amount of a row in words (5,000 Toman), when it is a number.
function amountText(r: Row): string {
  const v = asNumber(r.amount)
  return typeof v === 'number' && v > 0 ? money({ amount: v, currency: 'IRT' }, lang.value) : ''
}

// The chance of each row, while every weight typed is a number.
const total = computed(() => {
  const weights = rows.value.map((r) => asNumber(r.weight))
  if (weights.some((w) => typeof w !== 'number' || w < 1)) return 0
  const none = asNumber(props.noPrizeWeight ?? '')
  return (weights as number[]).reduce((a, b) => a + b, 0) + (typeof none === 'number' ? none : 0)
})
function chanceText(r: Row): string {
  const w = asNumber(r.weight)
  return total.value > 0 && typeof w === 'number' ? chancePercent(w / total.value, lang.value) : ''
}
</script>

<template>
  <div class="prizes" role="group" :aria-describedby="describedBy">
    <p v-if="!rows.length" class="app-muted empty">{{ t('settings.prizes.empty') }}</p>
    <div v-for="(r, i) in rows" :key="i" class="prize" role="group" :aria-label="t('settings.prizes.row', { n: num(i + 1, lang) })">
      <label class="cell">
        <span class="cap">{{ t('settings.prizes.amount') }}</span>
        <InputText
          :id="i === 0 ? inputId : undefined"
          :model-value="r.amount"
          dir="ltr"
          inputmode="numeric"
          maxlength="16"
          autocomplete="off"
          :readonly="locked"
          :invalid="invalid"
          class="input"
          @update:model-value="(v) => setField(i, 'amount', v)"
          @blur="emit('touch')"
        />
        <small v-if="amountText(r)" class="app-muted words">{{ amountText(r) }}</small>
      </label>
      <label class="cell">
        <span class="cap">{{ t('settings.prizes.weight') }}</span>
        <InputText
          :model-value="r.weight"
          dir="ltr"
          inputmode="numeric"
          maxlength="8"
          autocomplete="off"
          :readonly="locked"
          :invalid="invalid"
          class="input"
          @update:model-value="(v) => setField(i, 'weight', v)"
          @blur="emit('touch')"
        />
        <small v-if="chanceText(r)" class="app-muted words chance">{{ t('settings.prizes.chance', { percent: chanceText(r) }) }}</small>
      </label>
      <Button
        v-if="!locked"
        type="button"
        icon="pi pi-trash"
        severity="secondary"
        text
        rounded
        class="drop"
        :aria-label="t('settings.prizes.remove', { n: num(i + 1, lang) })"
        @click="remove(i)"
      />
    </div>
    <div v-if="!locked" class="foot">
      <Button
        :id="rows.length ? undefined : inputId"
        type="button"
        icon="pi pi-plus"
        :label="t('settings.prizes.add')"
        severity="secondary"
        outlined
        size="small"
        :disabled="!canAdd"
        @click="add"
      />
      <small class="app-muted">{{ rows.length >= MAX_PRIZES ? t('settings.prizes.max', { n: num(MAX_PRIZES, lang) }, MAX_PRIZES) : t('settings.prizes.sorted') }}</small>
    </div>
  </div>
</template>

<style scoped>
.prizes {
  display: grid;
  gap: 0.6rem;
}
.prize {
  display: grid;
  grid-template-columns: 1fr 1fr auto;
  gap: 0.6rem;
  align-items: start;
}
.cell {
  display: grid;
  gap: 0.2rem;
  min-inline-size: 0;
}
.cap {
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
}
.input {
  inline-size: 100%;
}
.words {
  font-size: 0.78rem;
}
.drop {
  margin-block-start: 1.35rem;
}
.foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
}
.empty {
  margin: 0;
  font-size: 0.9rem;
}
@media (max-width: 30rem) {
  .prize {
    grid-template-columns: 1fr auto;
  }
  .prize .cell:nth-child(2) {
    grid-column: 1;
  }
}
</style>
