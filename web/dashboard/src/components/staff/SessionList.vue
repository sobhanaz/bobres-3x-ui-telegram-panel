<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import Skeleton from 'primevue/skeleton'
import { deviceKind, deviceOf } from '../../staff'
import { dateTime, isolate, type Lang } from '../../format'
import type { SessionItem } from '../../types'

// Logged-in devices: a short device name, how and when it logged in, and a
// log-out button for every device but the one in use.
const props = defineProps<{ items: SessionItem[]; loading?: boolean; busy?: string; empty: string }>()
const emit = defineEmits<{ revoke: [s: SessionItem] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const rows = computed(() =>
  props.items.map((s) => {
    const d = deviceOf(s.user_agent)
    const name =
      d.browser && d.os
        ? t('account.device', { browser: isolate(d.browser), os: isolate(d.os) })
        : d.browser || d.os || t('account.device_unknown')
    const icon = { mobile: 'pi-mobile', tablet: 'pi-tablet', desktop: 'pi-desktop' }[deviceKind(d.os)]
    return { s, name, icon }
  }),
)
</script>

<template>
  <div v-if="loading && !items.length" class="skeletons">
    <Skeleton height="3rem" />
    <Skeleton height="3rem" />
  </div>
  <p v-else-if="!items.length" class="app-muted empty">{{ empty }}</p>
  <ul v-else class="sessions">
    <li v-for="r in rows" :key="r.s.id" class="session">
      <i :class="['pi', r.icon, 'icon']" aria-hidden="true" />
      <div class="body">
        <div class="name">
          <span :title="r.s.user_agent || undefined">{{ r.name }}</span>
          <Tag v-if="r.s.current" :value="t('account.this_device')" severity="success" />
        </div>
        <div class="app-muted meta">
          <span>{{ t('account.method.' + r.s.method) }}</span>
          <span v-if="r.s.ip" class="app-ltr">{{ r.s.ip }}</span>
          <span class="app-nowrap">{{ t('account.started', { time: dateTime(r.s.created_at, lang) }) }}</span>
          <span class="app-nowrap">{{ t('account.last_seen', { time: dateTime(r.s.last_seen_at, lang) }) }}</span>
        </div>
      </div>
      <Button
        v-if="!r.s.current"
        icon="pi pi-sign-out"
        severity="secondary"
        text
        rounded
        :loading="busy === r.s.id"
        :disabled="!!busy && busy !== r.s.id"
        :aria-label="t('account.revoke_device', { device: r.name })"
        v-tooltip.top="t('account.revoke')"
        @click="emit('revoke', r.s)"
      />
    </li>
  </ul>
</template>

<style scoped>
.skeletons {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.empty {
  margin: 0;
}
.sessions {
  list-style: none;
  margin: 0;
  padding: 0;
}
.session {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding-block: 0.6rem;
  border-block-end: 1px solid var(--p-content-border-color);
}
.session:last-child {
  border-block-end: 0;
}
.icon {
  font-size: 1.25rem;
  color: var(--p-text-muted-color);
  flex: none;
}
.body {
  flex: 1;
  min-inline-size: 0;
}
.name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  font-weight: 500;
}
.meta {
  display: flex;
  flex-wrap: wrap;
  gap: 0.15rem 0.9rem;
  font-size: 0.85rem;
}
</style>
