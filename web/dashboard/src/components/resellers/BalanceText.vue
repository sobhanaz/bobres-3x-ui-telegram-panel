<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { money, type Lang, type Money } from '../../format'
import { owed } from '../../resellers'

// A reseller's balance: below zero it reads "debt 50,000 Toman", never a
// bare minus sign (which Persian text would put on the wrong side).
const props = defineProps<{ value: Money }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const debt = computed(() => owed(props.value))
</script>

<template>
  <span v-if="debt" class="debt">{{ t('reseller.debt_amount', { amount: money(debt, lang) }) }}</span>
  <span v-else>{{ money(props.value, lang) }}</span>
</template>

<style scoped>
.debt {
  color: var(--p-red-500);
}
</style>
