<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { dateTime, type Lang } from '../../format'
import { DELIVERY_ICON, fileHref, fileSize, isImageFile, staffName } from '../../support'
import type { TicketMessageItem } from '../../types'

// One message of a thread: the customer's on the start side, staff replies on
// the end side, internal notes tinted and labelled. The body is the sender's
// own text: shown as text (never HTML), in its own direction.
const props = defineProps<{ message: TicketMessageItem; ticketId: string; customerLang: string }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const m = computed(() => props.message)
const href = computed(() => fileHref(props.ticketId, m.value))
const image = computed(() => !!m.value.file && isImageFile(m.value.file))
const author = computed(() => (m.value.author ? staffName(m.value.author) : ''))
const delivery = computed(() => (m.value.kind === 'staff' ? m.value.delivery : null))
const deliveryLabel = computed(() => (delivery.value ? t('support.delivery.' + delivery.value.status) : ''))
const deliveryTitle = computed(() => {
  const d = delivery.value
  if (!d) return ''
  return d.at ? `${deliveryLabel.value} · ${dateTime(d.at, lang.value)}` : deliveryLabel.value
})
const when = computed(() => new Date(m.value.created_at * 1000).toISOString())
</script>

<template>
  <article class="bubble" :class="m.kind" :aria-label="m.kind === 'note' ? t('support.note_label') : undefined">
    <header v-if="m.kind !== 'customer'" class="top">
      <span v-if="m.kind === 'note'" class="note-label"><i class="pi pi-lock" aria-hidden="true" /> {{ t('support.note_label') }}</span>
      <bdi v-if="author" class="author">{{ author }}</bdi>
      <span v-else-if="m.kind === 'staff'" class="author app-muted">{{ t('support.staff') }}</span>
    </header>

    <a v-if="m.file && image" :href="href" target="_blank" rel="noopener" class="photo" :title="t('support.open_file')">
      <img :src="href" :alt="m.file.name || t('support.photo')" loading="lazy" />
    </a>
    <a v-else-if="m.file" :href="href" :download="m.file.name || ''" class="file">
      <i class="pi pi-file" aria-hidden="true" />
      <span class="file-text">
        <bdi class="file-name">{{ m.file.name || t('support.file') }}</bdi>
        <span class="app-muted small">{{ fileSize(m.file.size, lang) }}</span>
      </span>
      <i class="pi pi-download" aria-hidden="true" />
    </a>

    <p v-if="m.body" class="body" dir="auto" :lang="m.kind === 'customer' ? customerLang || undefined : undefined">{{ m.body }}</p>

    <footer class="bottom">
      <time :datetime="when" class="small">{{ dateTime(m.created_at, lang) }}</time>
      <span
        v-if="delivery"
        class="delivery small"
        :class="delivery.status"
        role="img"
        :aria-label="deliveryLabel"
        :title="deliveryTitle"
      >
        <i :class="DELIVERY_ICON[delivery.status] ?? 'pi pi-question-circle'" aria-hidden="true" />
        <span v-if="delivery.status === 'blocked' || delivery.status === 'failed'" class="delivery-text">{{ deliveryLabel }}</span>
      </span>
    </footer>
  </article>
</template>

<style scoped>
.bubble {
  max-inline-size: min(36rem, 88%);
  padding: 0.55rem 0.8rem 0.4rem;
  border-radius: 0.9rem;
  border: 1px solid var(--p-content-border-color);
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  min-inline-size: 0;
}
.bubble.customer {
  align-self: flex-start;
  background: color-mix(in srgb, var(--p-text-color) 5%, var(--p-content-background));
  border-end-start-radius: 0.25rem;
}
.bubble.staff {
  align-self: flex-end;
  background: color-mix(in srgb, var(--p-primary-color) 13%, var(--p-content-background));
  border-color: color-mix(in srgb, var(--p-primary-color) 30%, var(--p-content-border-color));
  border-end-end-radius: 0.25rem;
}
.bubble.note {
  align-self: flex-end;
  background: color-mix(in srgb, var(--p-yellow-500) 13%, var(--p-content-background));
  border: 1px dashed color-mix(in srgb, var(--p-yellow-500) 55%, var(--p-content-border-color));
  border-end-end-radius: 0.25rem;
}
.top {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.25rem 0.6rem;
  font-size: 0.8rem;
}
.author {
  font-weight: 500;
}
.note-label {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  font-weight: 700;
  color: color-mix(in srgb, var(--p-yellow-500) 45%, var(--p-text-color));
}
.note-label i {
  font-size: 0.75rem;
}
.body {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.7;
}
.photo {
  display: block;
  inline-size: fit-content;
}
.photo img {
  display: block;
  max-inline-size: min(16rem, 100%);
  max-block-size: 16rem;
  border-radius: 0.6rem;
  object-fit: cover;
  background: var(--p-content-hover-background);
}
.file {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.5rem 0.65rem;
  border-radius: 0.6rem;
  border: 1px solid var(--p-content-border-color);
  background: var(--p-content-background);
  color: var(--p-text-color);
  text-decoration: none;
  min-inline-size: 0;
}
.file:hover {
  border-color: var(--p-primary-color);
}
.file-text {
  display: flex;
  flex-direction: column;
  min-inline-size: 0;
  flex: 1;
}
.file-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.bottom {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 0.4rem;
  color: var(--p-text-muted-color);
}
.small {
  font-size: 0.75rem;
}
.delivery {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
}
.delivery i {
  font-size: 0.75rem;
}
.delivery.sent {
  color: var(--p-primary-color);
}
.delivery.blocked,
.delivery.failed {
  color: var(--p-red-500);
  font-weight: 500;
}
</style>
