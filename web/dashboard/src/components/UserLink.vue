<script setup lang="ts">
import type { UserBrief } from '../types'

// A customer in a list or card: @username (or the Telegram id), linking to
// their page. The id stays left-to-right inside Persian text.
defineProps<{ user: UserBrief | null; link?: boolean }>()
</script>

<template>
  <span v-if="!user" class="app-muted">—</span>
  <RouterLink v-else-if="link !== false" :to="{ name: 'user', params: { id: user.id } }" class="who" @click.stop>
    <bdi>{{ user.username ? '@' + user.username : '' }}</bdi>
    <span class="app-ltr app-muted id">{{ user.telegram_id }}</span>
  </RouterLink>
  <span v-else class="who">
    <bdi>{{ user.username ? '@' + user.username : '' }}</bdi>
    <span class="app-ltr app-muted id">{{ user.telegram_id }}</span>
  </span>
</template>

<style scoped>
.who {
  display: inline-flex;
  gap: 0.4rem;
  align-items: baseline;
  color: inherit;
  text-decoration: none;
}
a.who:hover bdi {
  text-decoration: underline;
}
.id {
  font-size: 0.85em;
}
</style>
