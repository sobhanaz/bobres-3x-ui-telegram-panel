<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Tag from 'primevue/tag'
import { healthIcon, healthOf, healthSeverity } from '../../monitor'

// A component's state as a label: its colour, an icon and a word, so colour is never the only sign.
const props = defineProps<{ state: string }>()
const { t } = useI18n()
const health = computed(() => healthOf(props.state))
</script>

<template>
  <Tag :severity="healthSeverity[health]" :icon="healthIcon[health]" :value="t('monitor.state.' + health)" class="state-tag" />
</template>
