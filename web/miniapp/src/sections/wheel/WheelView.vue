<script setup lang="ts">
// The daily wheel: one free spin a day for a chance at wallet credit. Core decides everything (who may spin, the draw, the day's budget, once a day):
// this screen asks, then turns the wheel to the piece core named. Asking twice gives the first answer again, so a double tap pays once.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ApiError } from '../../api'
import { errorKey } from '../../errors'
import { fmtDateTime, fmtMoney, fmtNum } from '../../fmt'
import { lang, tr } from '../../i18n'
import { fail, loadWallet } from '../../store'
import { haptic } from '../../telegram'
import { wheelApi, type Spin, type Wheel } from './api'
import { chanceOf, restAngle, sectorPath, segmentFor, segmentsOf, targetRotation } from './wheel'

const SPIN_MS = 4200
const SIZE = 200
const R = 96

const data = ref<Wheel | null>(null)
const error = ref('')
const off = ref(false)
const busy = ref(false)
const spinning = ref(false)
const rotation = ref(0)
const result = ref<Spin | null>(null)
let gone = false
onBeforeUnmount(() => (gone = true))

async function load() {
  error.value = ''
  try {
    data.value = await wheelApi.get()
  } catch (e) {
    if (e instanceof ApiError && e.code === 'not_found') {
      off.value = true
      return
    }
    if (!fail(e)) error.value = tr(errorKey(e))
  }
}
onMounted(load)

const segments = computed(() => (data.value ? segmentsOf(data.value.prizes, data.value.no_prize_weight) : []))
const money = (amount: number) => (data.value ? fmtMoney(amount, data.value.currency, data.value.scale) : String(amount))
const percent = (fraction: number) => new Intl.NumberFormat(lang.value === 'fa' ? 'fa-IR' : 'en-US', { style: 'percent', maximumFractionDigits: 2 }).format(fraction)
/** label is the short text on a piece: the amount, or a dash for the piece that pays nothing. */
const label = (amount: number) => (amount > 0 ? fmtNum(amount) : '—')

const canSpin = computed(() => !!data.value && data.value.reason === '' && !data.value.spun && data.value.open && !busy.value)

const reduced = () => typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches
const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms))

/** play turns the wheel to the piece core's answer names. The tilt inside the piece is only for looks; the piece is core's. */
async function play(r: Spin) {
  const seg = segmentFor(segments.value, r.status === 'won' ? r.index : -1)
  if (!seg) return
  spinning.value = true
  rotation.value = targetRotation(rotation.value, restAngle(seg, Math.random()))
  await wait(reduced() ? 0 : SPIN_MS)
  spinning.value = false
}

async function spin() {
  if (!canSpin.value) return
  busy.value = true
  error.value = ''
  result.value = null
  try {
    const r = await wheelApi.spin()
    if (gone) return
    if (r.status === 'won' || r.status === 'lost') await play(r)
    if (gone) return
    result.value = r
    const d = data.value
    if (d) {
      if (r.status === 'won' || r.status === 'lost' || r.status === 'already') {
        d.prize = r.prize
        d.spun = true
      }
      if (r.status === 'closed') d.open = false
      if (r.status === 'orders' || r.status === 'role') {
        d.reason = r.status
        d.orders = r.orders
        d.min_orders = r.min_orders
      }
    }
    if (r.status === 'won') {
      haptic('success')
      void loadWallet().catch(() => {}) // the balance on the wallet tab, best effort
    }
  } catch (e) {
    if (e instanceof ApiError && e.code === 'not_found') {
      off.value = true
    } else if (!fail(e)) {
      error.value = tr(errorKey(e))
    }
  } finally {
    spinning.value = false
    busy.value = false
  }
}

/** The line under the wheel: what happened, or what stands in the way. */
const message = computed(() => {
  const d = data.value
  if (!d) return ''
  const r = result.value
  if (r) {
    switch (r.status) {
      case 'won':
        return tr('wheel.won', { amount: money(r.prize) })
      case 'lost':
        return tr('wheel.lost')
      case 'already':
        return tr('wheel.spun_today') + ' ' + (r.prize > 0 ? tr('wheel.result_won', { amount: money(r.prize) }) : tr('wheel.result_none'))
      case 'closed':
        return tr('wheel.closed')
      default:
        break
    }
  }
  if (d.reason === 'orders') return tr('wheel.need_orders', { need: fmtNum(d.min_orders), have: fmtNum(d.orders) })
  if (d.reason === 'role') return tr('wheel.role')
  if (d.spun) return tr('wheel.spun_today') + ' ' + (d.prize > 0 ? tr('wheel.result_won', { amount: money(d.prize) }) : tr('wheel.result_none'))
  if (!d.open) return tr('wheel.closed')
  return ''
})
const balance = computed(() => (result.value?.status === 'won' && data.value ? tr('wheel.balance', { balance: money(result.value.balance) }) : ''))
const next = computed(() => (data.value && data.value.spun && data.value.resets_at ? tr('wheel.next', { time: fmtDateTime(data.value.resets_at) }) : ''))

/** Where a piece's label sits: up at the rim of the piece, then turned with it, so the piece under the pointer reads straight. */
const labelAt = (start: number, end: number) => ({ x: SIZE / 2, y: SIZE / 2 - R * 0.66, rotate: (start + end) / 2 })
</script>

<template>
  <section>
    <h1>{{ tr('wheel.title') }}</h1>
    <p v-if="off" class="muted" role="status">{{ tr('wheel.off') }}</p>
    <p v-else-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>
    <p v-else-if="data === null" class="muted">{{ tr('common.loading') }}</p>
    <template v-else>
      <p class="muted">{{ tr('wheel.intro') }}</p>

      <div class="wheel-box" data-wheel>
        <svg class="pointer" viewBox="0 0 20 24" role="img" :aria-label="tr('wheel.pointer')"><path d="M10 24 L1 2 Q10 -2 19 2 Z" /></svg>
        <div
          class="wheel"
          data-wheel-disc
          :data-rotation="rotation"
          :style="{ transform: `rotate(${rotation}deg)`, transition: spinning ? `transform ${SPIN_MS}ms cubic-bezier(0.12, 0.6, 0.1, 1)` : 'none' }"
        >
          <svg :viewBox="`0 0 ${SIZE} ${SIZE}`" aria-hidden="true">
            <template v-if="segments.length === 1">
              <circle :cx="SIZE / 2" :cy="SIZE / 2" :r="R" class="seg-a" />
              <text :x="SIZE / 2" :y="SIZE / 2" text-anchor="middle" dominant-baseline="middle" class="t-a">{{ label(segments[0].amount) }}</text>
            </template>
            <template v-else>
              <g v-for="(s, i) in segments" :key="s.index" :data-segment="s.index">
                <path :d="sectorPath(SIZE / 2, SIZE / 2, R, s.start, s.end)" :class="s.index < 0 ? 'seg-none' : i % 2 ? 'seg-b' : 'seg-a'" />
                <text
                  v-if="s.end - s.start >= 16"
                  :x="labelAt(s.start, s.end).x"
                  :y="labelAt(s.start, s.end).y"
                  :transform="`rotate(${labelAt(s.start, s.end).rotate} ${SIZE / 2} ${SIZE / 2})`"
                  text-anchor="middle"
                  dominant-baseline="middle"
                  :class="s.index < 0 || i % 2 ? 't-b' : 't-a'"
                >{{ label(s.amount) }}</text>
              </g>
            </template>
          </svg>
        </div>
      </div>

      <section class="card" role="status" aria-live="polite">
        <p v-if="message" class="message" :class="{ pos: result?.status === 'won' }">{{ message }}</p>
        <p v-if="balance" class="muted">{{ balance }}</p>
        <p v-if="next" class="muted small">{{ next }}</p>
      </section>

      <button class="btn" type="button" :disabled="!canSpin" @click="spin">{{ busy ? tr('wheel.spinning') : tr('wheel.spin') }}</button>

      <section class="card">
        <h2>{{ tr('wheel.prizes') }}</h2>
        <p v-for="s in segments" :key="s.index" class="row between">
          <span>{{ s.index < 0 ? tr('wheel.none') : money(s.amount) }}</span>
          <span class="muted">{{ percent(chanceOf(s.weight, data.total_weight)) }}</span>
        </p>
      </section>
      <p class="muted small">{{ tr('wheel.fair') }}</p>
    </template>
  </section>
</template>

<style scoped>
.wheel-box { position: relative; inline-size: min(100%, 18rem); margin-inline: auto; padding-block-start: 0.75rem; }
.wheel { aspect-ratio: 1; inline-size: 100%; will-change: transform; }
.wheel svg { inline-size: 100%; block-size: 100%; display: block; }
.pointer { position: absolute; inset-block-start: 0; inset-inline: 0; margin-inline: auto; inline-size: 1.5rem; z-index: 1; fill: var(--danger); }
.seg-a { fill: var(--accent); }
.seg-b { fill: var(--card); stroke: var(--accent); stroke-width: 0.5; }
.seg-none { fill: var(--bg); stroke: var(--muted); stroke-width: 0.5; stroke-dasharray: 2 2; }
.t-a { fill: var(--accent-fg); font-size: 9px; font-weight: 600; }
.t-b { fill: var(--fg); font-size: 9px; font-weight: 600; }
.message { font-size: 1.1rem; font-weight: 600; }
@media (prefers-reduced-motion: reduce) { .wheel { transition: none !important; } }
</style>
