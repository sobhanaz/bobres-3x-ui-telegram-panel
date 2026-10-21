<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Password from 'primevue/password'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import Dialog from 'primevue/dialog'
import Textarea from 'primevue/textarea'
import Message from 'primevue/message'
import ToggleSwitch from 'primevue/toggleswitch'
import Tab from 'primevue/tab'
import TabList from 'primevue/tablist'
import TabPanel from 'primevue/tabpanel'
import TabPanels from 'primevue/tabpanels'
import Tabs from 'primevue/tabs'
import ProductDialog from '../components/supplier/ProductDialog.vue'
import SupplierBalance from '../components/supplier/SupplierBalance.vue'
import { api } from '../api'
import { errorText } from '../errors'
import { useList } from '../composables/useList'
import { dateTime, money, num, type Lang } from '../format'
import { costText, orderSeverity, orderStatuses, type SupplierOrder, type SupplierProduct, type SupplierStatus } from '../supplier'
import { keyPattern } from '../supplierkey'

// The owner's own store resells a supplier's digital accounts: the API key, the catalog, the price and texts of each
// product (written here, in Toman), and the orders.
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const fail = (e: unknown) => toast.add({ severity: 'error', summary: errorText(e, t).text, life: 6000 })

const status = ref<SupplierStatus>({ available: true, configured: false, on: false })
const loaded = ref(false)
async function loadStatus() {
  try {
    status.value = await api<SupplierStatus>('GET', '/supplier')
    loaded.value = true
  } catch (e) {
    fail(e)
  }
}

const key = ref('')
const keyOk = computed(() => keyPattern.test(key.value.trim()))
const saving = ref(false)
async function saveKey() {
  saving.value = true
  try {
    status.value = await api<SupplierStatus>('PUT', '/supplier/config', { api_key: key.value.trim() })
    key.value = '' // write-only: never shown again
    toast.add({ severity: 'success', summary: t('supplier.key_saved'), life: 3000 })
  } catch (e) {
    fail(e)
  } finally {
    saving.value = false
  }
}
async function setSales(on: boolean) {
  try {
    await api('PUT', '/settings', { values: { 'supplier.enabled': on ? 'true' : 'false' } })
    toast.add({ severity: 'success', summary: t(on ? 'supplier.turned_on' : 'supplier.turned_off'), life: 3000 })
    await loadStatus()
  } catch (e) {
    fail(e)
  }
}

// ---- Products
const products = ref<SupplierProduct[]>([])
const loadingProducts = ref(false)
async function loadProducts() {
  loadingProducts.value = true
  try {
    products.value = (await api<{ items: SupplierProduct[] }>('GET', '/supplier/products')).items
  } catch (e) {
    fail(e)
  } finally {
    loadingProducts.value = false
  }
}
const syncing = ref(false)
async function sync() {
  syncing.value = true
  try {
    const r = await api<{ count: number }>('POST', '/supplier/sync')
    toast.add({ severity: 'success', summary: t('supplier.synced', { n: num(r.count, lang.value) }, r.count), life: 3000 })
    await loadProducts()
  } catch (e) {
    fail(e)
  } finally {
    syncing.value = false
  }
}
const editing = ref<SupplierProduct | null>(null)
const editVisible = computed({ get: () => editing.value !== null, set: (v: boolean) => !v && (editing.value = null) })
const savingProduct = ref(false)
async function saveProduct(body: Record<string, unknown>) {
  if (!editing.value) return
  savingProduct.value = true
  try {
    const p = await api<SupplierProduct>('PUT', `/supplier/products/${encodeURIComponent(editing.value.id)}`, body)
    const i = products.value.findIndex((x) => x.id === p.id)
    if (i >= 0) products.value[i] = p
    editing.value = null
  } catch (e) {
    fail(e)
  } finally {
    savingProduct.value = false
  }
}
async function toggle(p: SupplierProduct, v: boolean) {
  try {
    const r = await api<SupplierProduct>('PUT', `/supplier/products/${encodeURIComponent(p.id)}`, { enabled: v })
    const i = products.value.findIndex((x) => x.id === r.id)
    if (i >= 0) products.value[i] = r
  } catch (e) {
    fail(e)
    await loadProducts() // the switch goes back to what core says
  }
}

// ---- Orders
const orders = useList<SupplierOrder, { status: string }>('/supplier/orders', { status: '' })
const statusOptions = computed(() => [
  { label: t('licenses.all'), value: '' },
  ...orderStatuses.map((s) => ({ label: t('supplier.order_status.' + s), value: s })),
])
const working = ref(false)
async function retry(o: SupplierOrder) {
  working.value = true
  try {
    await api('POST', `/supplier/orders/${o.id}/retry`)
    await orders.load()
  } catch (e) {
    fail(e)
  } finally {
    working.value = false
  }
}
const refunding = ref<SupplierOrder | null>(null)
const reason = ref('')
async function refund() {
  if (!refunding.value || reason.value.trim() === '') return
  working.value = true
  try {
    await api('POST', `/supplier/orders/${refunding.value.id}/refund`, { reason: reason.value.trim() })
    toast.add({ severity: 'success', summary: t('supplier.refunded'), life: 3000 })
    refunding.value = null
    reason.value = ''
    await orders.load()
  } catch (e) {
    fail(e)
  } finally {
    working.value = false
  }
}

const tab = ref('products')
onMounted(() => {
  void loadStatus()
  void loadProducts()
  void orders.load()
})
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.supplier.title') }}</h1>
  </div>
  <p class="app-muted intro">{{ t('supplier.intro') }}</p>
  <Message v-if="loaded && !status.available" severity="warn" :closable="false">{{ t('supplier.no_key') }}</Message>

  <div v-if="loaded && status.configured" class="balance"><SupplierBalance /></div>

  <section v-if="loaded" class="card">
    <h2 class="card-title">{{ t('supplier.connection') }}</h2>
    <p class="app-muted">{{ status.configured ? t('supplier.configured') : t('supplier.not_configured') }}</p>
    <form class="form" autocomplete="off" @submit.prevent="saveKey">
      <label class="field">
        <span>{{ t('supplier.api_key') }}</span>
        <Password v-model="key" class="app-ltr" :feedback="false" toggle-mask autocomplete="off" :invalid="key !== '' && !keyOk" />
        <small class="app-muted">{{ t('supplier.api_key_help') }}</small>
      </label>
      <div class="row">
        <Button type="submit" :label="t('licenses.save')" :loading="saving" :disabled="!keyOk || !status.available" />
      </div>
    </form>
    <div class="row">
      <Button v-if="status.configured && !status.on" severity="success" :label="t('supplier.turn_on')" @click="setSales(true)" />
      <Button v-if="status.on" severity="secondary" outlined :label="t('supplier.turn_off')" @click="setSales(false)" />
      <Tag v-if="status.on" severity="success" :value="t('supplier.on')" />
      <Tag v-else severity="secondary" :value="t('supplier.off')" />
    </div>
  </section>

  <Tabs v-model:value="tab" class="tabs">
    <TabList>
      <Tab value="products">{{ t('supplier.tab_products') }}</Tab>
      <Tab value="orders">{{ t('supplier.tab_orders') }}</Tab>
    </TabList>
    <TabPanels>
      <TabPanel value="products">
        <div class="app-toolbar">
          <Button icon="pi pi-refresh" outlined :label="t('supplier.sync')" :loading="syncing" :disabled="!status.configured" @click="sync" />
          <Button v-if="loadingProducts" text loading :aria-label="t('app.loading')" />
        </div>
        <p class="app-muted small">{{ t('supplier.products_help') }}</p>
        <DataTable :value="products" data-key="id" size="small" scrollable paginator :rows="25" :loading="loadingProducts">
          <template #empty>{{ t('supplier.no_products') }}</template>
          <Column :header="t('supplier.product')">
            <template #body="{ data: p }">
              <bdi class="name">{{ p.title.fa || p.title.en || p.name || p.id }}</bdi>
              <div class="app-muted small app-ltr app-mono">{{ p.id }}</div>
            </template>
          </Column>
          <Column :header="t('supplier.cost')" body-class="app-nowrap">
            <template #body="{ data: p }"><span class="app-ltr">{{ costText(p.cost_micro, p.cost_currency) || '—' }}</span></template>
          </Column>
          <Column :header="t('supplier.stock')" body-class="app-nowrap">
            <template #body="{ data: p }">
              <span v-if="p.stock !== null">{{ num(p.stock, lang) }}</span><span v-else class="app-muted">—</span>
              <Tag v-if="!p.listed" severity="warn" :value="t('supplier.unlisted')" class="tag" />
            </template>
          </Column>
          <Column :header="t('supplier.price')" body-class="app-nowrap">
            <template #body="{ data: p }">
              <span v-if="p.sell_price !== null">{{ money({ amount: p.sell_price, currency: 'IRT' }, lang) }}</span>
              <span v-else class="app-muted">{{ t('supplier.no_price') }}</span>
            </template>
          </Column>
          <Column :header="t('supplier.sell')">
            <template #body="{ data: p }">
              <ToggleSwitch :model-value="p.enabled" :disabled="p.sell_price === null && !p.enabled" :aria-label="t('supplier.sell')" @update:model-value="(v: boolean) => toggle(p, v)" />
            </template>
          </Column>
          <Column :header="t('licenses.actions')">
            <template #body="{ data: p }">
              <Button icon="pi pi-pencil" text rounded :aria-label="t('supplier.edit')" @click="editing = p" />
            </template>
          </Column>
        </DataTable>
      </TabPanel>
      <TabPanel value="orders">
        <div class="app-toolbar">
          <Select v-model="orders.filters.status" :options="statusOptions" option-label="label" option-value="value" :aria-label="t('licenses.status_col')" />
          <Button v-if="orders.loading.value" text loading :aria-label="t('app.loading')" />
        </div>
        <Message v-if="orders.error.value" severity="error" :closable="false">{{ orders.error.value }}</Message>
        <DataTable
          :value="orders.items.value"
          lazy
          :paginator="orders.total.value > orders.rows.value"
          :rows="orders.rows.value"
          :first="(orders.page.value - 1) * orders.rows.value"
          :total-records="orders.total.value"
          :loading="orders.loading.value"
          data-key="id"
          size="small"
          scrollable
          @page="orders.onPage"
        >
          <template #empty>{{ t('supplier.no_orders') }}</template>
          <Column :header="t('licenses.buyer')">
            <template #body="{ data: o }"><bdi v-if="o.buyer?.username">@{{ o.buyer.username }}</bdi><span v-else class="app-mono">{{ o.buyer?.telegram_id }}</span></template>
          </Column>
          <Column :header="t('supplier.product')"><template #body="{ data: o }"><bdi>{{ o.product }}</bdi></template></Column>
          <Column :header="t('supplier.price')" body-class="app-nowrap"><template #body="{ data: o }">{{ money(o.amount, lang) }}</template></Column>
          <Column :header="t('licenses.status_col')">
            <template #body="{ data: o }">
              <Tag :severity="orderSeverity(o.status)" :value="t('supplier.order_status.' + o.status)" />
              <div v-if="o.status === 'paid' && o.last_error" class="app-muted small"><bdi>{{ o.last_error }}</bdi> · {{ t('supplier.attempts', { n: num(o.attempts, lang) }, o.attempts) }}</div>
              <div v-else-if="o.status === 'refunded' && o.last_error" class="app-muted small"><bdi>{{ o.last_error }}</bdi></div>
            </template>
          </Column>
          <Column :header="t('supplier.date')" body-class="app-nowrap"><template #body="{ data: o }">{{ dateTime(o.created_at, lang) }}</template></Column>
          <Column :header="t('licenses.actions')">
            <template #body="{ data: o }">
              <div v-if="o.status === 'paid'" class="row">
                <Button text rounded icon="pi pi-replay" :loading="working" :aria-label="t('supplier.retry')" @click="retry(o)" />
                <Button text rounded severity="danger" icon="pi pi-undo" :aria-label="t('supplier.refund')" @click="refunding = o" />
              </div>
            </template>
          </Column>
        </DataTable>
      </TabPanel>
    </TabPanels>
  </Tabs>

  <ProductDialog v-model:visible="editVisible" :product="editing" :saving="savingProduct" @save="saveProduct" />

  <Dialog :visible="refunding !== null" modal :header="t('supplier.refund')" :style="{ width: '26rem' }" @update:visible="(v: boolean) => !v && (refunding = null)">
    <p class="app-muted">{{ t('supplier.refund_help') }}</p>
    <Textarea v-model="reason" rows="3" class="reason" :placeholder="t('licenses.reason')" :aria-label="t('licenses.reason')" maxlength="400" />
    <template #footer>
      <Button text :label="t('app.cancel')" @click="refunding = null" />
      <Button severity="danger" :label="t('supplier.refund')" :loading="working" :disabled="reason.trim() === ''" @click="refund" />
    </template>
  </Dialog>
</template>

<style scoped>
.balance {
  max-inline-size: 22rem;
  margin-block-end: 1rem;
}
.intro {
  margin-block: 0 0.75rem;
  font-size: 0.9rem;
}
.card {
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.75rem;
  padding: 1rem;
  margin-block-end: 1rem;
  display: grid;
  gap: 0.75rem;
}
.card-title {
  margin: 0;
  font-size: 1rem;
}
.form {
  display: grid;
  gap: 0.75rem;
  max-inline-size: 36rem;
}
.field {
  display: grid;
  gap: 0.25rem;
}
.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
.small {
  font-size: 0.8rem;
  margin: 0.25rem 0 0.5rem;
}
.name {
  font-weight: 600;
}
.tag {
  margin-inline-start: 0.4rem;
}
.reason {
  inline-size: 100%;
}
</style>
