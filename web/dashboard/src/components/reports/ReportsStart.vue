<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import { api } from '../../api'
import { errorText } from '../../errors'
import { dateTime, type Lang } from '../../format'
import { useAuth } from '../../stores/auth'

// Where the reports start counting. When the sales so far were tests, the owner starts the reports (and the overview's revenue) from zero:
// from that moment on they count only what happens next. Nothing is deleted (orders, payments, wallets and the ledger stay), and the owner can
// make the reports count everything again.
const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const auth = useAuth()
const confirm = useConfirm()
const toast = useToast()

const since = ref<number | null>(null)
const busy = ref(false)
const canReset = computed(() => auth.can('reports.reset'))

async function load() {
  try {
    since.value = (await api<{ since: number | null }>('GET', '/reports/since')).since
  } catch {
    since.value = null
  }
}
onMounted(load)

async function call(method: 'POST' | 'DELETE', done: string) {
  busy.value = true
  try {
    const out = await api<{ since: number | null }>(method, '/reports/reset', method === 'POST' ? { confirm: true } : undefined)
    since.value = out.since
    toast.add({ severity: 'success', summary: t(done), life: 6000 })
    emit('changed')
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 6000 })
  } finally {
    busy.value = false
  }
}

function start() {
  confirm.require({
    header: t('reports.start.confirm_title'),
    message: t('reports.start.confirm'),
    acceptProps: { label: t('reports.start.action'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: () => call('POST', 'reports.start.done'),
  })
}
function clear() {
  confirm.require({
    header: t('reports.start.clear'),
    message: t('reports.start.clear_confirm'),
    acceptProps: { label: t('reports.start.clear') },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: () => call('DELETE', 'reports.start.cleared'),
  })
}
</script>

<template>
  <p v-if="since !== null || canReset" class="app-muted hint start app-no-print" :class="{ 'is-set': since !== null }">
    <template v-if="since !== null">{{ t('reports.start.since', { when: dateTime(since, lang) }) }}</template>
    <template v-else>{{ t('reports.start.all') }}</template>
    <Button v-if="canReset && since === null" class="start-btn" :label="t('reports.start.action')" icon="pi pi-refresh" size="small" text :loading="busy" @click="start" />
    <Button v-if="canReset && since !== null" class="start-btn" :label="t('reports.start.restart')" icon="pi pi-refresh" size="small" text :loading="busy" @click="start" />
    <Button v-if="canReset && since !== null" class="start-clear" :label="t('reports.start.clear')" size="small" text severity="secondary" :loading="busy" @click="clear" />
  </p>
</template>

<style scoped>
.start {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.25rem 0.75rem;
}
.start.is-set {
  color: var(--p-primary-color);
}
</style>
