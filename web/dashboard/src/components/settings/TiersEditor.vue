<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import { asciiNumber, money, num, type Lang } from '../../format'
import { MAX_TIERS, normalizeTiers, parseTiers } from '../../settings'

// The tiers of the reseller targets (setting targets.tiers): rows of "services sold in a month" and "bonus in Toman". The setting is compact JSON; the
// rows keep what was typed, so a half-typed number is not lost, and the value sent up is the rows as JSON (a row that is not a number is sent as
// text, which the card then flags). Saved sorted by the number of services.
const props = defineProps<{ modelValue: string; inputId: string; invalid?: boolean; locked?: boolean; describedBy?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; touch: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

interface Row {
  threshold: string
  bonus: string
}

function rowsOf(text: string): Row[] {
  const p = parseTiers(text)
  return 'tiers' in p ? p.tiers.map((x) => ({ threshold: String(x.threshold), bonus: String(x.bonus) })) : []
}

const asNumber = (s: string): number | string => {
  const v = asciiNumber(s.trim())
  return /^\d{1,15}$/.test(v) ? Number(v) : s.trim()
}

/** draft is the rows as the value of the setting ("" for none). */
function draft(rs: Row[]): string {
  return rs.length ? JSON.stringify(rs.map((r) => ({ threshold: asNumber(r.threshold), bonus: asNumber(r.bonus) }))) : ''
}

const rows = ref<Row[]>(rowsOf(props.modelValue))
// A value that comes down from the card (a save, a reset) replaces the rows; the echo of our own edit does not.
watch(
  () => props.modelValue,
  (v) => {
    if (normalizeTiers(v) !== normalizeTiers(draft(rows.value))) rows.value = rowsOf(v)
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
  rows.value = [...rows.value, { threshold: '', bonus: '' }]
  changed()
  emit('touch')
}
function remove(i: number) {
  rows.value = rows.value.filter((_, j) => j !== i)
  changed()
  emit('touch')
}

const canAdd = computed(() => !props.locked && rows.value.length < MAX_TIERS)
// The bonus of a row in words (50,000 Toman), when it is a number.
function bonusText(r: Row): string {
  const v = asNumber(r.bonus)
  return typeof v === 'number' && v > 0 ? money({ amount: v, currency: 'IRT' }, lang.value) : ''
}
</script>

<template>
  <div class="tiers" role="group" :aria-describedby="describedBy">
    <p v-if="!rows.length" class="app-muted empty">{{ t('settings.tiers.empty') }}</p>
    <div v-for="(r, i) in rows" :key="i" class="tier" role="group" :aria-label="t('settings.tiers.row', { n: num(i + 1, lang) })">
      <label class="cell">
        <span class="cap">{{ t('settings.tiers.threshold') }}</span>
        <InputText
          :id="i === 0 ? inputId : undefined"
          :model-value="r.threshold"
          dir="ltr"
          inputmode="numeric"
          maxlength="7"
          autocomplete="off"
          :readonly="locked"
          :invalid="invalid"
          class="input"
          @update:model-value="(v) => setField(i, 'threshold', v)"
          @blur="emit('touch')"
        />
      </label>
      <label class="cell">
        <span class="cap">{{ t('settings.tiers.bonus') }}</span>
        <InputText
          :model-value="r.bonus"
          dir="ltr"
          inputmode="numeric"
          maxlength="16"
          autocomplete="off"
          :readonly="locked"
          :invalid="invalid"
          class="input"
          @update:model-value="(v) => setField(i, 'bonus', v)"
          @blur="emit('touch')"
        />
        <small v-if="bonusText(r)" class="app-muted words">{{ bonusText(r) }}</small>
      </label>
      <Button
        v-if="!locked"
        type="button"
        icon="pi pi-trash"
        severity="secondary"
        text
        rounded
        class="drop"
        :aria-label="t('settings.tiers.remove', { n: num(i + 1, lang) })"
        @click="remove(i)"
      />
    </div>
    <div v-if="!locked" class="foot">
      <Button
        :id="rows.length ? undefined : inputId"
        type="button"
        icon="pi pi-plus"
        :label="t('settings.tiers.add')"
        severity="secondary"
        outlined
        size="small"
        :disabled="!canAdd"
        @click="add"
      />
      <small class="app-muted">{{ rows.length >= MAX_TIERS ? t('settings.tiers.max', { n: num(MAX_TIERS, lang) }, MAX_TIERS) : t('settings.tiers.sorted') }}</small>
    </div>
  </div>
</template>

<style scoped>
.tiers {
  display: grid;
  gap: 0.6rem;
}
.tier {
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
  .tier {
    grid-template-columns: 1fr auto;
  }
  .tier .cell:nth-child(2) {
    grid-column: 1;
  }
}
</style>
