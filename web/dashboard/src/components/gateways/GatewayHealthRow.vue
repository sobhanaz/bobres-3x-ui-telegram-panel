<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Tag from 'primevue/tag'
import { dateTime, num, type Lang } from '../../format'
import { healthState } from '../../gateways'
import type { GatewayHealth } from '../../types'

// What an automated gateway's payments say about it: whether they go
// through, the last paid one, the open ones and the last error.
const props = defineProps<{ health: GatewayHealth | null | undefined }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const state = computed(() => healthState(props.health))
const open = computed(() => props.health?.open ?? 0)
</script>

<template>
  <div class="health" aria-live="polite">
    <Tag :severity="state === 'ok' ? 'success' : state === 'error' ? 'danger' : 'secondary'" :value="t('gateway.health.' + state)" class="health-state" />
    <span v-if="health?.last_paid_at" class="app-muted small">{{ t('gateway.last_paid', { time: dateTime(health.last_paid_at, lang) }) }}</span>
    <span v-if="open" class="app-muted small open-count">
      {{ t('gateway.open_count', { n: num(open, lang) }) }}<template v-if="health?.oldest_open_at">
        · {{ t('gateway.oldest_open', { time: dateTime(health.oldest_open_at, lang) }) }}</template>
    </span>
    <div v-if="health?.last_error" class="last-error small">
      <span class="app-muted">{{ health.last_error_at ? t('gateway.last_error_at', { time: dateTime(health.last_error_at, lang) }) : t('gateway.last_error') }}</span>
      <span class="app-ltr detail error-text">{{ health.last_error }}</span>
    </div>
  </div>
</template>

<style scoped>
.health {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.35rem 0.75rem;
  margin-block-end: 1rem;
}
.last-error {
  flex-basis: 100%;
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.25rem 0.5rem;
}
.small {
  font-size: 0.85rem;
}
.detail {
  font-size: 0.85rem;
  overflow-wrap: anywhere;
}
</style>
