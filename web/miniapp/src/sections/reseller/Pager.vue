<script setup lang="ts">
// Previous and Next, with the page count when it is known (a list that knows
// only whether another page follows passes hasMore instead of pages).
import { computed } from 'vue'
import { fmtNum } from '../../fmt'
import { tr } from '../../i18n'

const props = defineProps<{ page: number; pages?: number; hasMore?: boolean; busy?: boolean }>()
const emit = defineEmits<{ go: [page: number] }>()

const more = computed(() => (props.pages !== undefined ? props.page < props.pages : props.hasMore === true))
const show = computed(() => props.page > 1 || more.value)
</script>

<template>
  <nav v-if="show" class="rs-pager" :aria-label="tr('reseller.pages')">
    <button class="btn secondary" type="button" :disabled="busy || page <= 1" @click="emit('go', page - 1)">{{ tr('reseller.prev') }}</button>
    <span class="muted small">{{ pages !== undefined ? tr('reseller.page', { page: fmtNum(page), pages: fmtNum(pages) }) : fmtNum(page) }}</span>
    <button class="btn secondary" type="button" :disabled="busy || !more" @click="emit('go', page + 1)">{{ tr('reseller.next') }}</button>
  </nav>
</template>
