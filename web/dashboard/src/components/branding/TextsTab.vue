<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import IconField from 'primevue/iconfield'
import InputIcon from 'primevue/inputicon'
import Select from 'primevue/select'
import ToggleSwitch from 'primevue/toggleswitch'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import TextEditor from './TextEditor.vue'
import TextImport from './TextImport.vue'
import { api } from '../../api'
import { errorText } from '../../errors'
import { useAuth } from '../../stores/auth'
import { num, type Lang } from '../../format'
import { TEXT_LANGS, currentText, filterTexts, isChanged } from '../../branding'
import type { TextItem } from '../../types'

// Every bot text with its Persian and English version; a row opens the editor.
const props = defineProps<{ active: boolean }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const auth = useAuth()

const items = ref<TextItem[]>([])
const languages = ref<Lang[]>([...TEXT_LANGS])
const loading = ref(false)
const loadError = ref('')
let loaded = false
async function load() {
  loaded = true
  loading.value = true
  loadError.value = ''
  try {
    const res = await api<{ languages: string[]; items: TextItem[] }>('GET', '/texts')
    items.value = res.items
    const langs = TEXT_LANGS.filter((l) => res.languages.includes(l))
    if (langs.length) languages.value = langs
  } catch (e) {
    loaded = false
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
watch(
  () => props.active,
  (a) => {
    if (a && !loaded) void load()
  },
  { immediate: true },
)

const filters = reactive({ q: '', group: '', changed: false })
const filtered = computed(() => filterTexts(items.value, filters))
const first = ref(0)
watch(filters, () => (first.value = 0))

const groupLabel = (g: string) => (te('texts.group.' + g) ? t('texts.group.' + g) : g)
const contextLabel = (c: string) => (te('texts.context.' + c) ? t('texts.context.' + c) : c)
const groupOptions = computed(() => [
  { label: t('texts.all_groups'), value: '' },
  ...[...new Set(items.value.map((i) => i.group))]
    .map((g) => ({ label: groupLabel(g), value: g }))
    .sort((a, b) => a.label.localeCompare(b.label, lang.value)),
])
const changedCount = computed(() => items.value.filter(isChanged).length)
const changedLangs = (it: TextItem) => TEXT_LANGS.filter((l) => it.override[l])

// The editor.
const editing = ref<TextItem | null>(null)
const editorOpen = ref(false)
function edit(it: TextItem) {
  editing.value = it
  editorOpen.value = true
}
function saved(it: TextItem) {
  const i = items.value.findIndex((x) => x.key === it.key)
  if (i >= 0) items.value[i] = it
}
</script>

<template>
  <p class="app-muted intro">{{ t('texts.intro') }}</p>

  <div class="app-toolbar">
    <IconField class="app-grow">
      <InputIcon class="pi pi-search" />
      <InputText v-model="filters.q" :placeholder="t('texts.search')" :aria-label="t('texts.search')" fluid />
    </IconField>
    <Select
      v-model="filters.group"
      :options="groupOptions"
      option-label="label"
      option-value="value"
      :placeholder="t('texts.all_groups')"
      :aria-label="t('texts.group_label')"
    />
    <label class="toggle">
      <ToggleSwitch v-model="filters.changed" />
      <span>{{ t('texts.only_changed') }}</span>
    </label>
  </div>

  <div class="bar">
    <span class="app-muted count">
      {{ t('texts.count', { n: num(filtered.length, lang), total: num(items.length, lang) }) }}
      <template v-if="changedCount"> · {{ t('texts.changed_count', { n: num(changedCount, lang) }) }}</template>
    </span>
    <div class="app-actions">
      <a
        v-for="l in languages"
        :key="l"
        :href="`/api/v1/texts.json?lang=${l}`"
        download
        class="p-button p-component p-button-outlined p-button-secondary export"
        :title="t('texts.export_hint')"
      >
        <i class="pi pi-download" aria-hidden="true" />
        <span>{{ t('texts.export', { lang: t('texts.' + l) }) }}</span>
      </a>
      <TextImport @done="load" />
    </div>
  </div>

  <Message v-if="loadError" severity="error" :closable="false">
    {{ loadError }}
    <Button :label="t('app.retry')" text size="small" @click="load" />
  </Message>

  <DataTable
    v-model:first="first"
    :value="filtered"
    :loading="loading"
    data-key="key"
    size="small"
    scrollable
    row-hover
    paginator
    :rows="25"
    :rows-per-page-options="[25, 50, 100]"
    class="app-clickable"
    @row-click="(e) => edit(e.data as TextItem)"
  >
    <template #empty>{{ loading ? t('app.loading') : t('texts.none') }}</template>
    <Column :header="t('texts.key')">
      <template #body="{ data: it }">
        <div class="app-ltr app-mono key">{{ it.key }}</div>
        <div class="app-muted small">{{ groupLabel(it.group) }} · {{ contextLabel(it.context) }}</div>
      </template>
    </Column>
    <Column v-for="l in TEXT_LANGS" :key="l" :header="t('texts.' + l)">
      <template #body="{ data: it }">
        <span v-if="it.langs.includes(l)" class="cell" :class="'cell-' + l" :dir="l === 'fa' ? 'rtl' : 'ltr'" :lang="l">{{ currentText(it, l) }}</span>
        <span v-else class="app-muted small">{{ t('texts.not_used') }}</span>
      </template>
    </Column>
    <Column :header="t('common.status')">
      <template #body="{ data: it }">
        <i v-if="it.editable === false" v-tooltip="t('texts.owner_only')" class="pi pi-lock app-muted lock" :aria-label="t('texts.owner_only')" />
        <Tag v-if="isChanged(it)" :value="t('texts.changed')" severity="info" />
        <Tag v-else :value="t('texts.default')" severity="secondary" />
        <div v-if="isChanged(it)" class="app-muted small langs">{{ changedLangs(it).map((l) => t('texts.' + l)).join(lang === 'fa' ? '، ' : ', ') }}</div>
      </template>
    </Column>
    <Column>
      <template #body="{ data: it }">
        <Button icon="pi pi-pencil" text rounded :aria-label="t('texts.edit_named', { key: it.key })" @click.stop="edit(it)" />
      </template>
    </Column>
  </DataTable>

  <TextEditor v-model:visible="editorOpen" :item="editing" :brand="auth.me?.brand ?? ''" @saved="saved" />
</template>

<style scoped>
.intro {
  margin-block: 0 var(--app-gap);
}
.toggle {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  cursor: pointer;
}
.bar {
  display: flex;
  flex-wrap: wrap;
  gap: 0.6rem;
  align-items: center;
  justify-content: space-between;
  margin-block-end: 0.75rem;
}
.count {
  font-size: 0.875rem;
}
.export {
  gap: 0.5rem;
  text-decoration: none;
}
.key {
  font-weight: 700;
  overflow-wrap: anywhere;
}
.small {
  font-size: 0.8rem;
}
.cell {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  max-inline-size: 22rem;
  min-inline-size: 10rem;
  overflow-wrap: anywhere;
}
.cell-fa {
  font-family: var(--app-font-fa);
}
.cell-en {
  font-family: var(--app-font-en);
}
.langs {
  margin-block-start: 0.2rem;
}
</style>
