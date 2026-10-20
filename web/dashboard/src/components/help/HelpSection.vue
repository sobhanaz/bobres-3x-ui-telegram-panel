<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import ToggleSwitch from 'primevue/toggleswitch'
import Tag from 'primevue/tag'
import HelpEditor from './HelpEditor.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { isolate, num, type Lang } from '../../format'
import { HELP_LANGS, faqSize, firstLine, maxEntries, move } from '../../help'
import type { HelpItem, HelpKind, HelpLayout, HelpPage } from '../../types'

// One list of help content (the questions, or the tutorials): its entries in the
// order customers see them, each switched on or off, moved, edited, deleted;
// for the FAQ, how the bot lays it out; and a way to start from the defaults.
const props = defineProps<{ kind: HelpKind; page: HelpPage }>()
const emit = defineEmits<{ page: [page: HelpPage]; reload: [] }>()

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()

const items = computed(() => (props.kind === 'faq' ? props.page.faq : props.page.tutorials))
const limits = computed(() => props.page.limits)
const limit = computed(() => maxEntries(props.kind, limits.value))
const full = computed(() => items.value.length >= limit.value)
const onCount = computed(() => items.value.filter((i) => i.enabled).length)
const k = (name: string) => t(`help.${props.kind}.${name}`)
const named = (it: HelpItem) => isolate(it.title[lang.value] || it.title.fa || it.title.en)

const busy = ref(new Set<string>())
async function guard(id: string, fn: () => Promise<void>) {
  busy.value.add(id)
  try {
    await fn()
  } finally {
    busy.value.delete(id)
  }
}

/** stale is what a conflict (the list or the entry changed meanwhile) comes to: the list is loaded again. */
function stale() {
  toast.add({ severity: 'warn', summary: t('help.stale'), life: 5000 })
  emit('reload')
}

/** failed tells what went wrong. */
function failed(e: unknown) {
  if (e instanceof ApiError && e.status === 409 && e.code === 'state') {
    stale()
    return
  }
  const err = errorText(e, t)
  toast.add({ severity: 'error', summary: err.text, detail: err.detail || undefined, life: 6000 })
}

async function toggle(it: HelpItem, on: boolean) {
  await guard(it.id, async () => {
    try {
      await api('PUT', `/help/${it.id}/enabled`, { enabled: on, version: it.version })
      toast.add({ severity: 'success', summary: on ? t('help.shown_on') : t('help.shown_off'), life: 2500 })
      emit('reload')
    } catch (e) {
      failed(e)
    }
  })
}

async function moveBy(it: HelpItem, by: -1 | 1) {
  const ids = move(
    items.value.map((x) => x.id),
    it.id,
    by,
  )
  await guard(it.id, async () => {
    try {
      emit('page', await api<HelpPage>('POST', '/help/order', { kind: props.kind, ids }))
    } catch (e) {
      failed(e)
    }
  })
}

function remove(it: HelpItem) {
  confirm.require({
    header: t('help.delete.title'),
    message: t('help.delete.confirm', { title: named(it) }),
    acceptProps: { label: t('help.delete.title'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: () =>
      void guard(it.id, async () => {
        try {
          await api('DELETE', `/help/${it.id}`)
          toast.add({ severity: 'success', summary: t('help.deleted'), life: 3000 })
          emit('reload')
        } catch (e) {
          failed(e)
        }
      }),
  })
}

const startingFromDefaults = ref(false)
async function addDefaults() {
  startingFromDefaults.value = true
  try {
    emit('page', await api<HelpPage>('POST', '/help/defaults', { kind: props.kind }))
    toast.add({ severity: 'success', summary: t('help.defaults_done'), life: 3000 })
  } catch (e) {
    failed(e)
  } finally {
    startingFromDefaults.value = false
  }
}

// The editor.
const editing = ref<HelpItem | null>(null)
const editorOpen = ref(false)
function openEditor(it: HelpItem | null) {
  editing.value = it
  editorOpen.value = true
}

// How the bot lays out the FAQ in each language, with what the questions weigh.
const layoutText = (l: HelpLayout) => (l ? t('help.layout.' + l) : t('help.layout.none'))
const sizeOf = (l: Lang) => faqSize(items.value, l, limits.value)
</script>

<template>
  <section class="help-section" :aria-labelledby="'help-' + kind">
    <div class="head">
      <div class="titles">
        <h2 :id="'help-' + kind" class="title">{{ k('title') }}</h2>
        <p class="app-muted hint">{{ k('hint') }}</p>
        <p v-if="items.length" class="app-muted count">{{ t('help.count', { on: num(onCount, lang), total: num(items.length, lang) }) }}</p>
      </div>
      <div class="app-actions">
        <Button
          v-if="items.length === 0"
          :label="k('defaults')"
          icon="pi pi-download"
          severity="secondary"
          outlined
          :loading="startingFromDefaults"
          @click="addDefaults"
        />
        <span v-tooltip.top="full ? t('help.at_limit', { cap: num(limit, lang) }) : undefined">
          <Button :label="k('add')" icon="pi pi-plus" :disabled="full" @click="openEditor(null)" />
        </span>
      </div>
    </div>

    <div v-if="kind === 'faq'" class="layout">
      <strong class="layout-title">{{ t('help.layout.title') }}</strong>
      <ul>
        <li v-for="l in HELP_LANGS" :key="l" :data-lang="l">
          {{ t('help.layout.line', { lang: t('texts.' + l), layout: layoutText(page.faq_layout[l]) }) }}
          <template v-if="page.faq_layout[l]">
            ·
            {{ t('help.layout.size', { lang: t('texts.' + l), used: num(sizeOf(l).units, lang), cap: num(limits.one_message_units, lang), entries: num(limits.one_message_entries, lang) }) }}
          </template>
        </li>
      </ul>
      <small class="app-muted">{{ t('help.layout.rule', { entries: num(limits.one_message_entries, lang), cap: num(limits.one_message_units, lang) }) }}</small>
    </div>

    <DataTable :value="items" data-key="id" size="small" scrollable class="list">
      <template #empty>{{ k('empty') }}</template>
      <Column :header="t('help.col.order')" body-class="app-nowrap">
        <template #body="{ data: it, index }">
          <Button
            icon="pi pi-arrow-up"
            text
            rounded
            size="small"
            severity="secondary"
            :disabled="index === 0 || busy.has(it.id)"
            :aria-label="t('help.move_up', { title: named(it) })"
            @click="moveBy(it, -1)"
          />
          <Button
            icon="pi pi-arrow-down"
            text
            rounded
            size="small"
            severity="secondary"
            :disabled="index === items.length - 1 || busy.has(it.id)"
            :aria-label="t('help.move_down', { title: named(it) })"
            @click="moveBy(it, 1)"
          />
        </template>
      </Column>
      <Column :header="k('title_label')">
        <template #body="{ data: it }">
          <div v-for="l in HELP_LANGS" :key="l" class="line">
            <span v-if="it.title[l]" :dir="l === 'fa' ? 'rtl' : 'ltr'" :lang="l" :class="'t-' + l">{{ it.title[l] }}</span>
            <span v-else class="app-muted small">{{ t('texts.' + l) }}: {{ t('help.not_written') }}</span>
          </div>
          <Tag
            v-if="HELP_LANGS.some((l) => !it.title[l] || !it.body[l])"
            severity="secondary"
            :value="t('help.only', { lang: t('texts.' + (it.title.fa && it.body.fa ? 'fa' : 'en')) })"
          />
          <div v-if="it.body.fa || it.body.en" class="app-muted small first" dir="auto">{{ firstLine(it.body[lang] || it.body.fa || it.body.en) }}</div>
        </template>
      </Column>
      <Column v-if="kind === 'tutorial'" :header="t('help.col.buttons')" body-class="app-nowrap">
        <template #body="{ data: it }">
          <span v-if="it.buttons.length">{{ t('help.buttons_count', { n: num(it.buttons.length, lang) }, it.buttons.length) }}</span>
          <span v-else class="app-muted">—</span>
        </template>
      </Column>
      <Column :header="t('help.col.shown')" body-class="app-nowrap">
        <template #body="{ data: it }">
          <ToggleSwitch
            :model-value="it.enabled"
            :disabled="busy.has(it.id)"
            :aria-label="t('help.switch_named', { title: named(it) })"
            @update:model-value="(v: boolean) => toggle(it, v)"
          />
        </template>
      </Column>
      <Column body-class="app-nowrap">
        <template #body="{ data: it }">
          <Button icon="pi pi-pencil" text rounded size="small" :aria-label="t('help.edit_named', { title: named(it) })" @click="openEditor(it)" />
          <Button
            icon="pi pi-trash"
            text
            rounded
            size="small"
            severity="danger"
            :disabled="busy.has(it.id)"
            :aria-label="t('help.delete_named', { title: named(it) })"
            @click="remove(it)"
          />
        </template>
      </Column>
    </DataTable>

    <HelpEditor v-model:visible="editorOpen" :kind="kind" :item="editing" :limits="limits" @saved="emit('reload')" @stale="stale" />
  </section>
</template>

<style scoped>
.help-section {
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
  margin-block-end: 2.5rem;
}
.head {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  align-items: flex-start;
  justify-content: space-between;
}
.titles {
  flex: 1 1 22rem;
  min-inline-size: 0;
}
.title {
  margin: 0 0 0.25rem;
  font-size: 1.15rem;
}
.hint,
.count {
  margin: 0.2rem 0 0;
  font-size: 0.9rem;
}
.layout {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  padding: 0.6rem 0.9rem;
  border: 1px dashed var(--p-content-border-color);
  border-radius: 0.6rem;
  font-size: 0.9rem;
}
.layout ul {
  margin: 0;
  padding-inline-start: 1.2rem;
}
.line {
  max-inline-size: 28rem;
  overflow-wrap: anywhere;
}
.t-fa {
  font-family: var(--app-font-fa);
}
.t-en {
  font-family: var(--app-font-en);
}
.small {
  font-size: 0.8rem;
}
.first {
  max-inline-size: 28rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
