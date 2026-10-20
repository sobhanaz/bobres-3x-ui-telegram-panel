<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { isolate, num, type Lang } from '../../format'
import { ENTITY_FOR, problemArg } from '../../branding'
import type { HelpProblem } from '../../types'

// Why core (or the page) cannot take part of an entry, one line each, in the
// staff member's language. The codes of the help entries have texts of their
// own (help.problem.*); the ones the bot's texts have too (a tag, a length, a
// character) read as they do there.
const props = defineProps<{ problems: HelpProblem[] }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

function message(p: HelpProblem): string {
  const https = isolate('https://')
  const count = /^\d+$/.test(p.arg) ? Number(p.arg) : null
  const own = 'help.problem.' + p.code
  if (te(own)) return t(own, { arg: count !== null ? num(count, lang.value) : isolate(p.arg), https })
  // A limit is a count ("at most 1 character"): the text is pluralized by it.
  const limit = p.code === 'too_long' ? count : null
  const arg = problemArg(p)
  const shown = limit !== null ? num(limit, lang.value) : isolate(arg)
  const key = 'texts.problem.' + p.code
  const entity = isolate(ENTITY_FOR[p.arg] ?? '&amp;')
  return te(key) ? t(key, { arg: shown, entity }, limit ?? 0) : t('texts.problem.other', { code: p.code, arg: shown })
}
</script>

<template>
  <ul v-if="props.problems.length" class="problems" aria-live="polite">
    <li v-for="(p, i) in props.problems" :key="i">
      <i class="pi pi-exclamation-circle" aria-hidden="true" />
      <span>{{ message(p) }}</span>
    </li>
  </ul>
</template>

<style scoped>
.problems {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  /* Message's own error colour follows light and dark mode */
  color: var(--p-message-error-simple-color, var(--p-red-600));
  font-size: 0.875rem;
}
.problems li {
  display: flex;
  gap: 0.4rem;
  align-items: baseline;
}
.problems i {
  font-size: 0.8rem;
  flex: none;
}
</style>
