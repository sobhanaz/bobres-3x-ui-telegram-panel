<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { isolate, num, type Lang } from '../../format'
import { ENTITY_FOR, problemArg } from '../../branding'
import type { TextProblem } from '../../types'

// Why core refused a bot text, one line per problem, in the staff member's
// language. showKey adds which text it was (imports cover many).
const props = defineProps<{ problems: TextProblem[]; showKey?: boolean }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

function message(p: TextProblem): string {
  const arg = problemArg(p)
  const shown = p.code === 'too_long' && /^\d+$/.test(arg) ? num(Number(arg), lang.value) : isolate(arg)
  const key = 'texts.problem.' + p.code
  const entity = isolate(ENTITY_FOR[p.arg] ?? '&amp;')
  return te(key) ? t(key, { arg: shown, entity }) : t('texts.problem.other', { code: p.code, arg: shown })
}
</script>

<template>
  <ul v-if="props.problems.length" class="problems">
    <li v-for="(p, i) in props.problems" :key="i">
      <i class="pi pi-exclamation-circle" aria-hidden="true" />
      <span>
        <span v-if="showKey && p.key" class="app-ltr app-mono which">{{ p.lang ? p.lang + ' · ' : '' }}{{ p.key }}</span>
        {{ message(p) }}
      </span>
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
.which {
  margin-inline-end: 0.35rem;
  font-weight: 700;
}
</style>
