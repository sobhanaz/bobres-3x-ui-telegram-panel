<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Tag from 'primevue/tag'

// A ticket's status: waiting for staff (open), answered, closed.
const props = defineProps<{ status: string }>()
const { t, te } = useI18n()
const severity = computed(() => ({ open: 'warn', answered: 'info', closed: 'secondary' })[props.status] ?? 'secondary')
const label = computed(() => (te('support.status.' + props.status) ? t('support.status.' + props.status) : props.status))
</script>

<template>
  <Tag :severity="severity" :value="label" />
</template>
