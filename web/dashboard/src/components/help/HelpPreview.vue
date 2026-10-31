<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import TextPreview from '../branding/TextPreview.vue'
import { BOT_WORDS, readIn } from '../../help'
import type { ContentLang } from '../../format'
import type { HelpDraft, HelpLimits } from '../../types'

// Roughly how the bot shows an entry to a customer of one language: the message
// (a question's answer screen, or a tutorial) and the buttons under it. A
// customer whose language has no translation reads the other one, which this
// says. Nothing here is HTML for the browser: the text goes through the same
// reader as the bot texts' preview, the title as plain text.
const props = defineProps<{ draft: HelpDraft; lang: ContentLang; limits: HelpLimits }>()
const { t } = useI18n()

const reading = computed(() => readIn(props.draft, props.lang, props.limits))
const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
// The screen as the bot builds it: a question is headed with ❓, a tutorial with its title in bold.
const text = computed(() => {
  const r = reading.value
  if (!r) return ''
  return (props.draft.kind === 'faq' ? '❓ ' : '') + `<b>${esc(r.title)}</b>\n\n${r.body}`
})
const words = computed(() => BOT_WORDS[props.lang])
</script>

<template>
  <div class="help-preview" :lang="lang" data-testid="help-preview">
    <p v-if="!reading" class="app-muted note">{{ t('help.preview.nothing') }}</p>
    <template v-else>
      <p v-if="reading.lang !== lang" class="app-muted note fallback">
        {{ t('help.preview.fallback', { lang: t('texts.' + lang), other: t('texts.' + reading.lang) }) }}
      </p>
      <TextPreview :value="text" :html="true" :placeholders="[]" :lang="reading.lang" />
      <div class="keys" :dir="lang === 'fa' ? 'rtl' : 'ltr'">
        <template v-if="draft.kind === 'tutorial'">
          <span v-for="(b, i) in reading.buttons" :key="i" class="key link">{{ b.label }}</span>
        </template>
        <span v-else class="key primary">{{ words.support }}</span>
        <span class="pair">
          <span class="key">{{ words.back }}</span>
          <span class="key">{{ words.home }}</span>
        </span>
      </div>
      <p class="app-muted note">{{ t('help.preview.note') }}</p>
    </template>
  </div>
</template>

<style scoped>
.help-preview {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  min-inline-size: 0;
}
.note {
  margin: 0;
  font-size: 0.8rem;
}
.keys {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}
.pair {
  display: flex;
  gap: 0.3rem;
}
.key {
  flex: 1;
  text-align: center;
  padding: 0.4rem 0.6rem;
  border-radius: 0.5rem;
  background: var(--p-content-hover-background);
  border: 1px solid var(--p-content-border-color);
  font-size: 0.85rem;
  overflow-wrap: anywhere;
}
.key.primary {
  background: var(--p-blue-600, #2563eb);
  border-color: transparent;
  color: #fff;
}
.key.link::after {
  content: ' ↗';
  white-space: pre;
  unicode-bidi: isolate;
}
</style>
