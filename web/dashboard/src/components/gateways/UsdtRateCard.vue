<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Card from 'primevue/card'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import { api } from '../../api'
import { errorText } from '../../errors'
import { dateTime, num, type Lang } from '../../format'
import type { UsdtRate } from '../../types'

// What the store charges for one USDT now: the live price (read by the bot every couple of minutes) plus the margin, or the owner's own rate
// when the live one is off or cannot be read. The switch, the margin and the own rate are in the card below; this one shows the result.
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const info = ref<UsdtRate | null>(null)
const error = ref('')
const busy = ref(false)
async function load() {
  try {
    info.value = await api<UsdtRate>('GET', '/usdt-rate')
    error.value = ''
  } catch (e) {
    error.value = errorText(e, t).text
  }
}
async function refresh() {
  busy.value = true
  try {
    info.value = await api<UsdtRate>('POST', '/usdt-rate/refresh')
    error.value = ''
    toast.add({ severity: 'success', summary: t('usdtrate.refreshed'), life: 3000 })
  } catch (e) {
    toast.add({ severity: 'error', summary: t('usdtrate.refresh_failed', { why: errorText(e, t).text }), life: 8000 })
  } finally {
    busy.value = false
  }
}
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void load()
  timer = setInterval(() => void load(), 60_000)
})
onBeforeUnmount(() => clearInterval(timer))
const rateText = computed(() => (info.value && info.value.rate > 0 ? t('usdtrate.per_usdt', { n: num(info.value.rate, lang.value) }) : t('usdtrate.none')))
const sourceSeverity = computed(() => (info.value?.source === 'live' ? 'success' : info.value?.source === 'manual' ? 'info' : 'warn'))
</script>

<template>
  <Card>
    <template #title>{{ t('usdtrate.title') }}</template>
    <template #content>
      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
      <div v-else-if="info" class="rate">
        <div class="now">
          <strong class="num">{{ rateText }}</strong>
          <Tag :severity="sourceSeverity" :value="t('usdtrate.source.' + (info.source || 'none'))" />
        </div>
        <p v-if="info.live" class="app-muted small">
          {{ t('usdtrate.live_line', { n: num(info.live.rate, lang), when: dateTime(info.live.at, lang), sources: info.live.sources.join(', ') }) }}
          <span v-if="!info.live.fresh"> · {{ t('usdtrate.stale') }}</span>
        </p>
        <p v-if="info.auto && info.margin > 0" class="app-muted small">{{ t('usdtrate.margin_line', { n: num(info.margin, lang) }) }}</p>
        <p v-if="!info.auto" class="app-muted small">{{ t('usdtrate.off_line') }}</p>
        <p v-if="!info.live && info.auto" class="app-muted small">{{ t('usdtrate.not_read') }}</p>
        <Button :label="t('usdtrate.refresh')" icon="pi pi-refresh" size="small" outlined :loading="busy" @click="refresh" />
      </div>
    </template>
  </Card>
</template>

<style scoped>
.rate {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  align-items: flex-start;
}
.now {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
  font-size: 1.15rem;
}
.small {
  margin: 0;
  font-size: 0.875rem;
}
</style>
