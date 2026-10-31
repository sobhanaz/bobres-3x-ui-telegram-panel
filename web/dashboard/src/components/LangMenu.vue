<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Menu from 'primevue/menu'
import Tag from 'primevue/tag'
import { setLang } from '../i18n'
import { BETA_LANGS, LANGS, languageName, rtl, type Lang } from '../format'

// The dashboard's language: each in its own words, the machine-translated
// ones marked beta (a native speaker has not reviewed them yet).
const { t, locale } = useI18n()
const menu = ref<InstanceType<typeof Menu> | null>(null)
const current = computed(() => locale.value as Lang)
const items = computed(() =>
  LANGS.map((l) => ({ key: l, label: languageName(l), beta: BETA_LANGS.includes(l), command: () => setLang(l) })),
)
</script>

<template>
  <Button
    :label="languageName(current)"
    icon="pi pi-globe"
    text
    size="small"
    aria-haspopup="true"
    aria-controls="lang-menu"
    :aria-label="t('lang.menu')"
    data-test="lang-menu"
    @click="(e: Event) => menu?.toggle(e)"
  />
  <Menu id="lang-menu" ref="menu" :model="items" popup>
    <template #item="{ item, props }">
      <a v-bind="props.action" class="lang-item" :data-lang="item.key" :aria-current="item.key === current ? 'true' : undefined">
        <span :lang="item.key" :dir="rtl(item.key as Lang) ? 'rtl' : 'ltr'">{{ item.label }}</span>
        <Tag v-if="item.beta" :value="t('lang.beta')" severity="warn" :title="t('lang.beta_note')" />
        <i v-if="item.key === current" class="pi pi-check check" aria-hidden="true" />
      </a>
    </template>
  </Menu>
</template>

<style scoped>
.lang-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.check {
  margin-inline-start: auto;
}
</style>
