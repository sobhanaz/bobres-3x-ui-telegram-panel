<script setup lang="ts">
// Digital accounts, the way the bot sells them: a category, a product page, how many, a summary, the wallet pays, and the account
// is shown (and kept in My purchases). A purchase is charged once per key, so a double tap or a retry after a lost answer is the same order.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ApiError } from '../../api'
import { errorKey, gateFor } from '../../errors'
import { fmtDateTime, fmtMoney, fmtNum } from '../../fmt'
import { newKey } from '../../format'
import { lang, planName, tr } from '../../i18n'
import { fail } from '../../store'
import { copyText, haptic } from '../../telegram'
import { digitalApi, type DigitalItem, type DigitalOrder, type DigitalShelf } from './api'

const emit = defineEmits<{ wallet: [] }>()

type Step = 'categories' | 'products' | 'product' | 'quantity' | 'summary' | 'result' | 'mine' | 'order'
const QUICK = [1, 2, 3, 5, 10, 15, 20, 25]
const SLOW_AFTER = 80 // polls, 3 seconds apart

const shelf = ref<DigitalShelf | null>(null)
const step = ref<Step>('categories')
const category = ref('')
const item = ref<DigitalItem | null>(null)
const quantity = ref(1)
const typed = ref('')
const key = ref('')
const order = ref<DigitalOrder | null>(null)
const mine = ref<DigitalOrder[] | null>(null)
const busy = ref(false)
const error = ref('')
const note = ref('')
const copied = ref(false)
const slow = ref(false)
let timer: ReturnType<typeof setInterval> | undefined

function stop() {
  if (timer) clearInterval(timer)
  timer = undefined
}
onBeforeUnmount(stop)

function oops(e: unknown) {
  if (fail(e)) return
  // a price or stock that moved is not an error of the customer's: show the shelf again as it is now
  if (e instanceof ApiError && (e.code === 'state' || e.code === 'conflict')) {
    note.value = tr('digital.changed')
    void load().then(() => {
      step.value = shelf.value && category.value && categories.value.some((c) => c.id === category.value) ? 'products' : 'categories'
    })
    return
  }
  error.value = tr(errorKey(e))
  haptic('error')
}

async function load() {
  error.value = ''
  try {
    shelf.value = await digitalApi.shelf()
    const cats = categories.value
    if (cats.length === 1 && step.value === 'categories') {
      category.value = cats[0].id
      step.value = 'products'
    }
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) shelf.value = { items: [], wallet: null, currency: 'IRT', scale: 0, max_quantity: 25 }
    else oops(e)
  }
}
onMounted(load)

const name = (names: Record<string, string>) => planName(names, lang.value)
const describe = (it: DigitalItem) => planName(it.descriptions, lang.value)
const money = (amount: number) => fmtMoney(amount, shelf.value?.currency ?? 'IRT', shelf.value?.scale ?? 0)

// The supplier's categories, in the order its products come; a product with none is under "Other".
const categories = computed(() => {
  const out: { id: string; label: string; count: number }[] = []
  for (const it of shelf.value?.items ?? []) {
    const found = out.find((c) => c.id === it.category)
    if (found) found.count++
    else out.push({ id: it.category, label: it.category || tr('digital.other'), count: 1 })
  }
  return out
})
const products = computed(() => (shelf.value?.items ?? []).filter((it) => it.category === category.value))

const maxQuantity = computed(() => {
  const max = shelf.value?.max_quantity ?? 25
  const it = item.value
  return it && it.stock_known ? Math.max(0, Math.min(max, it.stock)) : max
})
const total = computed(() => (item.value?.price ?? 0) * quantity.value)
const spendable = computed(() => (shelf.value?.wallet?.balance ?? 0) + (shelf.value?.wallet?.credit_limit ?? 0))
const short = computed(() => Math.max(0, total.value - spendable.value))

function openCategory(id: string) {
  category.value = id
  step.value = 'products'
}
function openProduct(it: DigitalItem) {
  item.value = it
  quantity.value = 1
  typed.value = ''
  step.value = 'product'
}
function pickQuantity(n: number) {
  quantity.value = n
  typed.value = ''
  toSummary()
}
function typedQuantity() {
  const n = Number(typed.value.replace(/[۰-۹]/g, (d) => String('۰۱۲۳۴۵۶۷۸۹'.indexOf(d))).trim())
  if (!Number.isInteger(n) || n < 1 || n > maxQuantity.value) {
    error.value = tr('digital.qty_bad', { max: fmtNum(maxQuantity.value) })
    return
  }
  error.value = ''
  quantity.value = n
  toSummary()
}
function toSummary() {
  error.value = ''
  key.value = newKey()
  step.value = 'summary'
}

async function pay() {
  if (!item.value || busy.value || short.value > 0) return
  busy.value = true
  error.value = ''
  note.value = ''
  try {
    const o = await digitalApi.buy({ sid: item.value.sid, quantity: quantity.value, expected_total: total.value, key: key.value })
    order.value = o
    step.value = 'result'
    haptic('success')
    key.value = ''
    // the wallet changed: the next screen of the shelf shows the new balance
    void load()
    if (o.status === 'paid') poll(o.id)
  } catch (e) {
    oops(e)
  } finally {
    busy.value = false
  }
}

// An order the supplier has not delivered yet is asked for again every few seconds.
function poll(id: string) {
  stop()
  slow.value = false
  let n = 0
  timer = setInterval(async () => {
    if (++n > SLOW_AFTER) {
      stop()
      slow.value = true
      return
    }
    try {
      const o = await digitalApi.order(id)
      if (order.value?.id === id) order.value = o
      if (o.status !== 'paid') stop()
    } catch (e) {
      if (gateFor(e)) {
        stop()
        fail(e)
      }
    }
  }, 3000)
}

async function openMine() {
  error.value = ''
  step.value = 'mine'
  mine.value = null
  try {
    mine.value = await digitalApi.orders()
  } catch (e) {
    oops(e)
  }
}
async function openOrder(o: DigitalOrder) {
  error.value = ''
  order.value = o
  step.value = 'order'
  try {
    order.value = await digitalApi.order(o.id)
    if (order.value.status === 'paid') poll(o.id)
  } catch (e) {
    oops(e)
  }
}

async function copy() {
  try {
    await copyText(order.value?.delivery ?? '')
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  } catch {
    error.value = tr('err.unknown')
  }
}

function back() {
  error.value = ''
  stop()
  const to: Record<Step, Step> = {
    categories: 'categories', products: 'categories', product: 'products', quantity: 'product', summary: 'quantity',
    result: 'categories', mine: 'categories', order: 'mine',
  }
  step.value = to[step.value]
  if (step.value === 'categories' && categories.value.length === 1) {
    category.value = categories.value[0].id
    step.value = 'products'
  }
}
const showBack = computed(() => step.value !== 'categories' && !(step.value === 'products' && categories.value.length === 1))
</script>

<template>
  <section>
    <h1>{{ tr('digital.title') }}</h1>
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>
    <p v-if="note" class="muted" role="status">{{ note }}</p>
    <button v-if="showBack" class="link" type="button" @click="back">{{ tr('digital.back') }}</button>

    <p v-if="shelf === null && !error" class="muted">{{ tr('common.loading') }}</p>

    <template v-else-if="shelf">
      <p v-if="shelf.wallet && step !== 'result' && step !== 'order'" class="muted">{{ tr('digital.balance', { amount: money(shelf.wallet.balance) }) }}</p>

      <!-- categories -->
      <template v-if="step === 'categories'">
        <p v-if="!shelf.items.length" class="muted">{{ tr('digital.empty') }}</p>
        <template v-else>
          <h2>{{ tr('digital.pick_category') }}</h2>
          <ul class="list">
            <li v-for="c in categories" :key="c.id">
              <button class="card row between" type="button" @click="openCategory(c.id)">
                <span><strong>{{ c.label }}</strong> <span class="muted small">{{ tr('digital.count', { n: fmtNum(c.count) }) }}</span></span>
                <span class="chev" aria-hidden="true">›</span>
              </button>
            </li>
          </ul>
        </template>
        <button class="btn secondary" type="button" @click="openMine">{{ tr('digital.mine') }}</button>
      </template>

      <!-- the products of a category -->
      <template v-else-if="step === 'products'">
        <ul class="list">
          <li v-for="it in products" :key="it.sid">
            <button class="card row between" type="button" @click="openProduct(it)">
              <span>
                <strong>{{ name(it.names) }}</strong>
                <span class="muted small block">{{ money(it.price) }}</span>
              </span>
              <span v-if="it.sold_out" class="badge expired">{{ tr('digital.sold_out') }}</span>
              <span v-else class="chev" aria-hidden="true">›</span>
            </button>
          </li>
        </ul>
        <button v-if="categories.length === 1" class="btn secondary" type="button" @click="openMine">{{ tr('digital.mine') }}</button>
      </template>

      <!-- the product page -->
      <section v-else-if="step === 'product' && item" class="card">
        <strong>{{ name(item.names) }}</strong>
        <p v-if="describe(item)" class="group-text">{{ describe(item) }}</p>
        <p>{{ tr('digital.price', { price: money(item.price) }) }}</p>
        <p v-if="item.stock_known && !item.sold_out" class="muted small">{{ tr('digital.stock', { n: fmtNum(item.stock) }) }}</p>
        <p v-if="item.sold_out" class="badge expired">{{ tr('digital.sold_out') }}</p>
        <button v-else class="btn" type="button" @click="step = 'quantity'">{{ tr('digital.buy') }}</button>
      </section>

      <!-- how many -->
      <section v-else-if="step === 'quantity' && item" class="card">
        <strong>{{ name(item.names) }}</strong>
        <h2>{{ tr('digital.qty_title') }}</h2>
        <div class="row wrap">
          <button v-for="n in QUICK.filter((q) => q <= maxQuantity)" :key="n" class="btn secondary" type="button" @click="pickQuantity(n)">{{ fmtNum(n) }}</button>
        </div>
        <label class="field">
          <span class="muted small">{{ tr('digital.qty_custom', { max: fmtNum(maxQuantity) }) }}</span>
          <span class="row">
            <input v-model="typed" inputmode="numeric" dir="ltr" autocomplete="off" @keydown.enter="typedQuantity" />
            <button class="btn" type="button" :disabled="!typed.trim()" @click="typedQuantity">{{ tr('digital.continue') }}</button>
          </span>
        </label>
      </section>

      <!-- the summary -->
      <section v-else-if="step === 'summary' && item" class="card">
        <strong>{{ tr('digital.summary') }}</strong>
        <p class="total"><span>{{ tr('digital.line_product') }}</span><span>{{ name(item.names) }}</span></p>
        <p class="total"><span>{{ tr('digital.line_qty') }}</span><span>{{ fmtNum(quantity) }}</span></p>
        <p class="total"><span>{{ tr('digital.line_unit') }}</span><span>{{ money(item.price) }}</span></p>
        <p class="total"><strong>{{ tr('digital.line_total') }}</strong><strong>{{ money(total) }}</strong></p>
        <p v-if="!short" class="total muted"><span>{{ tr('digital.line_after') }}</span><span>{{ money(spendable - total) }}</span></p>
        <template v-if="short">
          <p class="error">{{ tr('digital.short', { short: money(short) }) }}</p>
          <button class="btn" type="button" @click="emit('wallet')">{{ tr('digital.topup') }}</button>
        </template>
        <button v-else class="btn" type="button" :disabled="busy" @click="pay">{{ busy ? tr('digital.paying') : tr('digital.pay') }}</button>
      </section>

      <!-- the result of a purchase, or one order opened from My purchases -->
      <section v-else-if="(step === 'result' || step === 'order') && order" class="card" role="status">
        <p v-if="order.status === 'done'">{{ tr('digital.done') }}</p>
        <p v-else-if="order.status === 'refunded'">{{ tr('digital.refunded') }}</p>
        <p v-else>{{ slow ? tr('digital.pending_slow') : tr('digital.pending') }}</p>
        <p class="muted small">{{ tr('digital.order_line', { name: name(order.names), n: fmtNum(order.quantity) }) }} · {{ money(order.total.amount) }}</p>
        <template v-if="order.status === 'done' && order.delivery">
          <pre class="mono" dir="ltr" style="white-space: pre-wrap; margin: 0">{{ order.delivery }}</pre>
          <button class="btn" type="button" @click="copy">{{ copied ? tr('digital.copied') : tr('digital.copy') }}</button>
        </template>
        <button v-if="step === 'result'" class="btn secondary" type="button" @click="openMine">{{ tr('digital.mine') }}</button>
      </section>

      <!-- my purchases -->
      <template v-else-if="step === 'mine'">
        <h2>{{ tr('digital.mine') }}</h2>
        <p v-if="mine === null && !error" class="muted">{{ tr('common.loading') }}</p>
        <p v-else-if="mine && !mine.length" class="muted">{{ tr('digital.mine_empty') }}</p>
        <ul v-else class="list">
          <li v-for="o in mine ?? []" :key="o.id">
            <button class="card row between" type="button" @click="openOrder(o)">
              <span>
                <strong>{{ tr('digital.order_line', { name: name(o.names), n: fmtNum(o.quantity) }) }}</strong>
                <span class="muted small block">{{ money(o.total.amount) }} · {{ fmtDateTime(o.at) }}</span>
              </span>
              <span class="badge" :class="{ active: o.status === 'done', expired: o.status === 'refunded' }">{{ tr('digital.status.' + o.status) }}</span>
            </button>
          </li>
        </ul>
      </template>
    </template>
  </section>
</template>
