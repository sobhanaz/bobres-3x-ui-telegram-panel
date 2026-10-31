<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { gregorianLocale, type Lang } from '../../format'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Skeleton from 'primevue/skeleton'
import { api } from '../../api'
import { errorText } from '../../errors'
import { balanceLine, balanceRefreshMs, type SupplierBalanceAnswer } from '../../supplier'

// The owner's balance at the supplier, always in front of them: read when the page opens and again every couple of
// minutes while it is open (core shows a balance read in the last minute again, so this is not a call to the supplier
// each time). `quiet` (the overview) shows nothing at all when there is no balance to show: no key yet, or the supplier
// does not answer.
const props = defineProps<{ quiet?: boolean }>()
const { t, locale } = useI18n()

const answer = ref<SupplierBalanceAnswer | null>(null)
const error = ref('')
const loading = ref(true)
const at = ref<Date | null>(null)
let timer: ReturnType<typeof setInterval> | undefined

async function load(fresh = false) {
  loading.value = true
  try {
    answer.value = await api<SupplierBalanceAnswer>('GET', '/supplier/balance' + (fresh ? '?fresh=1' : ''))
    error.value = ''
    at.value = new Date()
  } catch (e) {
    error.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  void load()
  timer = setInterval(() => {
    if (!document.hidden) void load()
  }, balanceRefreshMs)
})
onBeforeUnmount(() => clearInterval(timer))

const time = () =>
  at.value ? new Intl.DateTimeFormat(gregorianLocale(locale.value as Lang), { timeStyle: 'short' }).format(at.value) : ''
// nothing to show in the quiet card: still loading the first time is not "nothing"
const hidden = () => props.quiet && !loading.value && !balanceLine(answer.value)
</script>

<template>
  <Card v-if="!hidden()" class="stat supplier-balance">
    <template #subtitle>{{ t('supplier.balance_title') }}</template>
    <template #content>
      <div v-if="balanceLine(answer)" class="big app-ltr app-mono">{{ balanceLine(answer) }}</div>
      <Skeleton v-else-if="loading" height="3rem" />
      <div v-else-if="answer" class="mid">{{ t('supplier.balance_unknown') }}</div>
      <div v-else class="app-muted">{{ error }}</div>
      <p v-if="answer && !balanceLine(answer) && answer.raw" class="app-muted raw">
        {{ t('supplier.balance_raw') }}
        <code class="app-ltr app-mono">{{ answer.raw }}</code>
      </p>
      <div class="foot">
        <span v-if="at" class="app-muted">{{ t('supplier.balance_at', { time: time() }) }}</span>
        <span v-if="error && answer" class="app-muted">{{ error }}</span>
        <Button
          icon="pi pi-refresh"
          text
          rounded
          size="small"
          :loading="loading"
          :aria-label="t('supplier.balance_refresh')"
          @click="load(true)"
        />
      </div>
    </template>
  </Card>
</template>

<style scoped>
.big {
  font-size: 1.8rem;
  font-weight: 600;
}
.mid {
  font-size: 1.1rem;
}
.raw {
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  margin-block-start: 0.35rem;
  font-size: 0.85rem;
}
</style>
