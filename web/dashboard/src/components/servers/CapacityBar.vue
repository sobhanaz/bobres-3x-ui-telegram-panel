<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ProgressBar from 'primevue/progressbar'
import Tag from 'primevue/tag'
import { num, type Lang } from '../../format'
import { capacityOf } from '../../servers'

// A server's services against its limit: "N / max" with a bar, and a chip from 90% on (a full server is passed
// over when a new service is made, and a plan that names only full servers waits). Without a limit it is the count.
// quiet: the server is switched off and takes no new services anyway, so the fill is shown without a warning.
const props = defineProps<{ clients: number; max?: number; quiet?: boolean }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const filled = computed(() => capacityOf({ clients: props.clients, max_clients: props.max }))
const cap = computed(() => (props.quiet && ['near', 'full'].includes(filled.value.state) ? { ...filled.value, state: 'ok' as const } : filled.value))
const label = computed(() => t('server.capacity_label', { percent: num(cap.value.pct, lang.value) }))
</script>

<template>
  <span v-if="cap.state === 'none'" class="clients">{{ num(cap.clients, lang) }}</span>
  <div v-else class="capacity" :class="'capacity-' + cap.state">
    <span class="clients" dir="ltr">{{ num(cap.clients, lang) }} / {{ num(cap.max, lang) }}</span>
    <ProgressBar :value="Math.min(100, cap.pct)" :show-value="false" :aria-label="label" :class="['bar', cap.state]" />
    <Tag v-if="cap.state === 'near'" severity="warn" icon="pi pi-exclamation-triangle" :value="t('server.capacity_near', { percent: num(cap.pct, lang) })" v-tooltip.top="t('server.capacity_near_hint')" class="chip near" />
    <Tag v-else-if="cap.state === 'full'" severity="danger" icon="pi pi-ban" :value="t('server.capacity_full')" v-tooltip.top="t('server.capacity_full_hint')" class="chip full" />
  </div>
</template>

<style scoped>
.capacity {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 0.25rem;
  min-inline-size: 8rem;
}
.clients {
  white-space: nowrap;
}
.bar {
  inline-size: 100%;
  block-size: 0.35rem;
}
.bar.near :deep(.p-progressbar-value) {
  background: var(--p-orange-500);
}
.bar.full :deep(.p-progressbar-value) {
  background: var(--p-red-500);
}
.chip {
  font-size: 0.75rem;
}
</style>
