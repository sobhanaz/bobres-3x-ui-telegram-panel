<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Tag from 'primevue/tag'

// A status as a colored label. kind picks the vocabulary.
const props = defineProps<{ kind: 'order' | 'service' | 'payment' | 'user' | 'review'; status: string }>()
const { t, te } = useI18n()

type Severity = 'success' | 'info' | 'warn' | 'danger' | 'secondary' | 'contrast'
const severities: Record<string, Record<string, Severity>> = {
  order: { created: 'secondary', awaiting_payment: 'warn', paid: 'info', provisioning: 'info', active: 'success',
    provision_failed: 'danger', cancelled: 'secondary', expired: 'secondary' },
  service: { pending: 'info', active: 'success', expiring_soon: 'warn', expired: 'secondary', disabled: 'danger',
    depleted: 'warn', deleted: 'secondary' },
  payment: { pending: 'secondary', confirming: 'warn', succeeded: 'success', failed: 'danger', expired: 'secondary' },
  user: { active: 'success', banned: 'danger' },
  review: { approved: 'success', rejected: 'danger' },
}
const severity = computed(() => severities[props.kind]?.[props.status] ?? 'secondary')
const label = computed(() => {
  const key = `${props.kind}.status.${props.status}`
  return te(key) ? t(key) : props.status
})
</script>

<template>
  <Tag :severity="severity" :value="label" />
</template>
