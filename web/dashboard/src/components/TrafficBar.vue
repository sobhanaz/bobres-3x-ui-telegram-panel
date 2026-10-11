<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ProgressBar from 'primevue/progressbar'
import { bytes, type Lang } from '../format'

// Traffic used out of the quota (total null = unlimited).
const props = defineProps<{ used: number; total: number | null }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const pct = computed(() => (props.total ? Math.min(100, Math.round((props.used * 100) / props.total)) : 0))
const level = computed(() => (pct.value >= 100 ? 'full' : pct.value >= 80 ? 'high' : 'ok'))
</script>

<template>
  <div class="traffic">
    <div class="text">
      <template v-if="total">{{ t('service.traffic_of', { used: bytes(used, lang), total: bytes(total, lang) }) }}</template>
      <template v-else>{{ t('service.traffic_unlimited', { used: bytes(used, lang) }) }}</template>
    </div>
    <ProgressBar v-if="total" :value="pct" :show-value="false" :class="['bar', level]" />
  </div>
</template>

<style scoped>
.traffic {
  min-inline-size: 9rem;
}
.text {
  font-size: 0.85rem;
  white-space: nowrap;
}
.bar {
  block-size: 0.35rem;
  margin-block-start: 0.25rem;
}
.bar.high :deep(.p-progressbar-value) {
  background: var(--p-orange-500);
}
.bar.full :deep(.p-progressbar-value) {
  background: var(--p-red-500);
}
</style>
