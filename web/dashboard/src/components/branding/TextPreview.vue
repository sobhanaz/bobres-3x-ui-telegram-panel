<script setup lang="ts">
import { computed, h, type FunctionalComponent, type VNodeChild } from 'vue'
import { parsePreview, sampleValue, type PreviewNode } from '../../branding'
import type { Lang } from '../../format'

// Roughly how Telegram shows a bot text: the allowed tags as Vue elements,
// the {placeholders} as sample values, everything else as plain text. It
// never renders the text as HTML.
const props = withDefaults(
  defineProps<{
    value: string
    html: boolean
    placeholders: string[]
    lang: Lang
    brand?: string
    kind?: 'message' | 'button' | 'toast'
  }>(),
  { brand: '', kind: 'message' },
)

const nodes = computed(() => parsePreview(props.value.trim(), props.html, props.placeholders))

function render(list: PreviewNode[]): VNodeChild[] {
  return list.map((n) => {
    if (n.type === 'text') return n.text
    if (n.type === 'ph') return h('span', { class: 'ph', title: '{' + n.name + '}' }, sampleValue(n.name, props.lang, props.brand))
    const children = render(n.children)
    if (n.tag === 'spoiler') return h('span', { class: 'spoiler' }, children)
    if (n.tag === 'a') return h('span', { class: 'link', title: n.href }, children)
    return h(n.tag, children)
  })
}
const Nodes: FunctionalComponent = () => render(nodes.value)
</script>

<template>
  <div class="preview" :class="kind" :dir="lang === 'fa' ? 'rtl' : 'ltr'" :lang="lang"><Nodes /></div>
</template>

<style scoped>
.preview {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.6;
  font-size: 0.9rem;
  min-block-size: 1.6em;
}
.preview[lang='fa'] {
  font-family: var(--app-font-fa);
}
.preview[lang='en'] {
  font-family: var(--app-font-en);
}
.message {
  padding: 0.6rem 0.8rem;
  border-radius: 0.9rem;
  border-end-start-radius: 0.25rem;
  background: var(--p-content-hover-background);
  border: 1px solid var(--p-content-border-color);
}
.button {
  display: block;
  text-align: center;
  padding: 0.45rem 0.9rem;
  border-radius: 0.5rem;
  background: var(--p-content-hover-background);
  border: 1px solid var(--p-content-border-color);
  font-weight: 500;
}
.toast {
  display: block;
  padding: 0.5rem 0.9rem;
  border-radius: 0.6rem;
  background: var(--p-surface-800);
  color: var(--p-surface-0);
}
.preview :deep(.ph) {
  display: inline-block;
  padding-inline: 0.35rem;
  border-radius: 0.35rem;
  background: var(--p-highlight-background);
  color: var(--p-highlight-color);
  white-space: nowrap;
  unicode-bidi: isolate;
}
.preview :deep(.spoiler) {
  filter: blur(0.3em);
  transition: filter 0.2s;
}
.preview :deep(.spoiler:hover) {
  filter: none;
}
.preview :deep(.link) {
  text-decoration: underline;
  color: var(--p-primary-color);
}
.preview :deep(code),
.preview :deep(pre) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.85em;
  background: var(--p-content-background);
  border-radius: 0.3rem;
}
.preview :deep(code) {
  padding-inline: 0.25rem;
}
.preview :deep(pre) {
  margin: 0.25rem 0;
  padding: 0.4rem 0.6rem;
  white-space: pre-wrap;
}
.preview :deep(blockquote) {
  margin: 0.25rem 0;
  padding-inline-start: 0.6rem;
  border-inline-start: 3px solid var(--p-primary-color);
}
</style>
