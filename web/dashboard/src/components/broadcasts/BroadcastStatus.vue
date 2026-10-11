<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Tag from 'primevue/tag'
import type { BroadcastStatus } from '../../broadcasts'

// A broadcast's status as a coloured label.
const props = defineProps<{ status: BroadcastStatus | string }>()
const { t, te } = useI18n()

type Severity = 'success' | 'info' | 'warn' | 'danger' | 'secondary' | 'contrast'
const severities: Record<string, Severity> = {
  draft: 'secondary',
  scheduled: 'contrast',
  running: 'info',
  paused: 'warn',
  done: 'success',
  cancelled: 'secondary',
}
const icons: Record<string, string> = {
  draft: 'pi pi-pencil',
  scheduled: 'pi pi-clock',
  running: 'pi pi-send',
  paused: 'pi pi-pause',
  done: 'pi pi-check',
  cancelled: 'pi pi-times',
}
const label = computed(() => {
  const key = 'broadcasts.status.' + props.status
  return te(key) ? t(key) : props.status
})
</script>

<template>
  <Tag :severity="severities[props.status] ?? 'secondary'" :value="label" :icon="icons[props.status]" />
</template>
