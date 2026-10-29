<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import ReasonDialog from '../ReasonDialog.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { dateTime, decimalOf, num, type Lang } from '../../format'
import { depositKey, depositNoteMax, resolveBody, tonviewerUrl, tronscanUrl, unmatchedReason, type DepositNetwork } from '../../gateways'
import type { UnmatchedDeposit } from '../../types'

// USDT (or, with network TON, TON) that reached the store's address but
// matched no payment (another amount, no comment, or too late), whatever its
// age: nothing was credited, so staff find the customer, adjust their balance
// by hand, then mark the deposit handled. A handled deposit leaves the list,
// and payments refuses its transaction id as a manual proof, so the same USDT
// is never credited twice. Amounts are exact, in Latin digits, as the
// customer's wallet shows them. A TON deposit also shows the comment it came
// with (a payment's memo, or anything the sender wrote) and links to Tonviewer.
const props = defineProps<{
  /** the newest deposits still to handle */
  items: UnmatchedDeposit[]
  /** how many there are in all (the list may hold fewer) */
  count?: number
  /** TON for the deposits of automatic TON; none for USDT on TRON */
  network?: DepositNetwork
}>()
// A deposit was handled, or changed meanwhile: the list is read again.
const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()

const isTon = computed(() => props.network === 'TON')
// Both cards can be on one page, so each section has an id of its own.
const titleId = computed(() => (isTon.value ? 'ton-unmatched-title' : 'usdt-unmatched-title'))
const explorerUrl = (txid: string) => (isTon.value ? tonviewerUrl(txid) : tronscanUrl(txid))

function reasonOf(d: UnmatchedDeposit): { text: string; ltr: boolean } | null {
  const r = unmatchedReason(d, props.network)
  if (!r) return null
  return 'key' in r ? { text: t(r.key), ltr: false } : { text: r.raw, ltr: true }
}

// The newest first; one transaction may carry more than one transfer.
const rows = computed(() =>
  [...props.items].sort((a, b) => b.at - a.at).map((d) => ({ ...d, key: depositKey(d), url: explorerUrl(d.txid), why: reasonOf(d) })),
)
const total = computed(() => Math.max(props.count ?? 0, props.items.length))

async function copy(txid: string) {
  try {
    await navigator.clipboard.writeText(txid)
    toast.add({ severity: 'info', summary: t('common.copied'), life: 2000 })
  } catch {
    /* clipboard blocked: the id is selectable */
  }
}

// Marking one handled: what it is, an optional note (audited), a confirm.
const target = ref<UnmatchedDeposit | null>(null)
const dialog = ref(false)
const busy = ref(false)
const error = ref('')
const detail = ref('')

function ask(d: UnmatchedDeposit) {
  target.value = d
  error.value = ''
  detail.value = ''
  dialog.value = true
}

async function resolve(note: string) {
  const d = target.value
  if (!d || busy.value) return
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    await api('POST', '/gateways/deposits/resolve', resolveBody(d, note, props.network))
    dialog.value = false
    toast.add({ severity: 'success', summary: t('gateway.usdt.resolved'), life: 3000 })
    emit('changed')
  } catch (e) {
    const err = errorText(e, t)
    error.value = err.text
    detail.value = err.detail
    // Handled by someone else, or claimed by a manual proof: read it again.
    if (e instanceof ApiError && (e.status === 404 || e.status === 409)) emit('changed')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="unmatched" :aria-labelledby="titleId">
    <div class="head">
      <h3 :id="titleId" class="title">{{ t('gateway.usdt.unmatched') }}</h3>
      <Tag v-if="total" :value="num(total, lang)" severity="warn" class="unmatched-count" />
    </div>
    <p class="app-muted help">{{ t(isTon ? 'gateway.ton.unmatched_help' : 'gateway.usdt.unmatched_help') }}</p>
    <p v-if="total > rows.length" class="app-muted help unmatched-more">
      {{ t('gateway.usdt.unmatched_more', { shown: num(rows.length, lang), n: num(total, lang) }) }}
    </p>
    <DataTable :value="rows" data-key="key" size="small" scrollable class="unmatched-table">
      <template #empty>{{ t('gateway.usdt.unmatched_empty') }}</template>
      <Column :header="t('common.date')" body-class="app-nowrap">
        <template #body="{ data: d }">{{ dateTime(d.at, lang) }}</template>
      </Column>
      <Column :header="t('common.amount')">
        <template #body="{ data: d }">
          <span class="app-ltr app-mono app-nowrap amount">{{ decimalOf(d.amount) }} {{ d.amount.currency }}</span>
          <div v-if="d.why" :class="['app-muted', 'small', 'reason', { 'app-ltr': d.why.ltr, 'reason-wide': isTon }]">{{ d.why.text }}</div>
        </template>
      </Column>
      <Column :header="t('gateway.usdt.from')">
        <template #body="{ data: d }">
          <span class="app-ltr app-mono small clip from" :title="d.from">{{ d.from }}</span>
        </template>
      </Column>
      <Column v-if="isTon" :header="t('gateway.ton.comment')">
        <template #body="{ data: d }">
          <span v-if="d.comment" class="app-ltr app-mono small clip comment" :title="d.comment">{{ d.comment }}</span>
          <span v-else class="app-muted small no-comment">{{ t('gateway.ton.comment_none') }}</span>
        </template>
      </Column>
      <Column :header="t('payment.txid')">
        <template #body="{ data: d }">
          <!-- the actions first: a long id is cut on a phone -->
          <div class="txid-cell">
            <Button icon="pi pi-copy" text rounded size="small" :aria-label="t('gateway.usdt.copy_txid')" class="copy" @click="copy(d.txid)" />
            <a
              v-if="d.url"
              :href="d.url"
              target="_blank"
              rel="noopener noreferrer"
              :aria-label="t(isTon ? 'gateway.ton.tonviewer' : 'gateway.usdt.tronscan')"
              :title="t(isTon ? 'gateway.ton.tonviewer' : 'gateway.usdt.tronscan')"
              :class="['explorer', isTon ? 'tonviewer' : 'tronscan']"
            >
              <i class="pi pi-external-link" aria-hidden="true" />
            </a>
            <span class="app-ltr app-mono small clip txid" :title="d.txid">{{ d.txid }}</span>
          </div>
        </template>
      </Column>
      <Column body-class="app-nowrap">
        <template #body="{ data: d }">
          <Button :label="t('gateway.usdt.resolve')" icon="pi pi-check" size="small" severity="secondary" outlined class="resolve" @click="ask(d)" />
        </template>
      </Column>
    </DataTable>

    <ReasonDialog
      v-model:visible="dialog"
      :title="t('gateway.usdt.resolve_title')"
      :message="t(isTon ? 'gateway.ton.resolve_message' : 'gateway.usdt.resolve_message')"
      :confirm-label="t('gateway.usdt.resolve')"
      :busy="busy"
      :error="error"
      :detail="detail"
      :maxlength="depositNoteMax"
      reason-optional
      @confirm="resolve"
    >
      <dl v-if="target" class="app-facts resolve-facts">
        <dt>{{ t('common.amount') }}</dt>
        <dd><span class="app-ltr app-mono amount">{{ decimalOf(target.amount) }} {{ target.amount.currency }}</span></dd>
        <dt>{{ t('gateway.usdt.from') }}</dt>
        <dd><span class="app-ltr app-mono small wrap">{{ target.from }}</span></dd>
        <template v-if="isTon">
          <dt>{{ t('gateway.ton.comment') }}</dt>
          <dd>
            <span v-if="target.comment" class="app-ltr app-mono small wrap resolve-comment">{{ target.comment }}</span>
            <span v-else class="app-muted">{{ t('gateway.ton.comment_none') }}</span>
          </dd>
        </template>
        <dt>{{ t('payment.txid') }}</dt>
        <dd><span class="app-ltr app-mono small wrap">{{ target.txid }}</span></dd>
      </dl>
    </ReasonDialog>
  </section>
</template>

<style scoped>
.head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.title {
  margin: 0;
  font-size: 1rem;
  font-weight: 700;
}
.help {
  font-size: 0.82rem;
  line-height: 1.6;
  margin-block: 0.35rem 0.5rem;
}
.amount {
  font-weight: 600;
}
/* with the comment column the reason would run down a narrow strip */
.reason-wide {
  min-inline-size: 13rem;
}
.small {
  font-size: 0.8rem;
}
.clip {
  display: inline-block;
  max-inline-size: 9rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}
.wrap {
  overflow-wrap: anywhere;
}
.txid-cell {
  display: flex;
  align-items: center;
  gap: 0.25rem;
}
.explorer {
  display: inline-flex;
  padding: 0.35rem;
}
</style>
