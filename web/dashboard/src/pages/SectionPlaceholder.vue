<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import Card from 'primevue/card'
import { sections } from '../sections'
import { num, type Lang } from '../format'

const { t, locale } = useI18n()
const route = useRoute()
const section = computed(() => sections.find((s) => s.key === route.meta.section))
</script>

<template>
  <template v-if="section">
    <h1 class="app-page-title">{{ t('section.' + section.key + '.title') }}</h1>
    <Card>
      <template #content>
        <div class="row">
          <i :class="section.icon" class="icon" aria-hidden="true" />
          <div>
            <p class="desc">{{ t('section.' + section.key + '.desc') }}</p>
            <p class="app-muted">{{ t('placeholder.coming', { n: num(section.milestone, locale as Lang) }) }}</p>
            <p class="app-muted">{{ t('placeholder.today') }}</p>
          </div>
        </div>
      </template>
    </Card>
  </template>
</template>

<style scoped>
.row {
  display: flex;
  gap: 1rem;
  align-items: flex-start;
}
.icon {
  font-size: 1.8rem;
  color: var(--p-primary-color);
  margin-block-start: 0.2rem;
}
.desc {
  margin-block-start: 0;
  font-weight: 500;
}
</style>
