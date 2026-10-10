<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ProgressBar from 'primevue/progressbar'
import { num, type Lang } from '../../format'
import { percent, progress, sending } from '../../broadcasts'
import type { BroadcastItem } from '../../types'

// Sent out of all recipients, with a thin bar while the broadcast is going
// (the bar counts every recipient already handled: sent, blocked, failed).
const props = defineProps<{ item: Pick<BroadcastItem, 'status' | 'counts'> }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const p = computed(() => progress(props.item.counts))
const started = computed(() => props.item.status !== 'draft' && props.item.status !== 'scheduled')
const title = computed(() =>
  t('broadcasts.progress.breakdown', {
    sent: num(props.item.counts.sent, lang.value),
    blocked: num(props.item.counts.blocked, lang.value),
    failed: num(props.item.counts.failed + props.item.counts.unknown, lang.value),
  }),
)
</script>

<template>
  <div v-if="started" class="progress" :title="title">
    <div class="nums">
      {{ num(item.counts.sent, lang) }} / {{ num(item.counts.total, lang) }}
      <span v-if="sending(item.status)" class="app-muted">· {{ percent(p.percent, lang) }}</span>
    </div>
    <ProgressBar
      v-if="sending(item.status)"
      :value="p.percent"
      :show-value="false"
      class="bar"
      :class="{ paused: item.status === 'paused' }"
      :aria-label="t('broadcasts.progress.label')"
    />
  </div>
  <span v-else class="app-muted">—</span>
</template>

<style scoped>
.progress {
  min-inline-size: 7rem;
}
.nums {
  font-size: 0.85rem;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.bar {
  block-size: 0.35rem;
  margin-block-start: 0.25rem;
}
.bar.paused :deep(.p-progressbar-value) {
  background: var(--p-orange-500);
}
</style>
