<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Card from 'primevue/card'
import Button from 'primevue/button'
import Message from 'primevue/message'
import SettingField from './SettingField.vue'
import { api } from '../../api'
import { errorText } from '../../errors'
import { num, type Lang } from '../../format'
import {
  canonical,
  changes,
  dependsOn,
  dirtyKeys,
  effective,
  example,
  fieldId,
  normalize,
  problems,
  rebase,
  serverField,
  validate,
  type Problem,
} from '../../settings'
import type { SettingItem, SettingsReply } from '../../types'

// One group of settings with its own Save: only the keys that changed are
// sent, and core answers every setting, which the page passes back in.
const props = defineProps<{ group: string; items: SettingItem[] }>()
const emit = defineEmits<{ saved: [reply: SettingsReply] }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()

const drafts = ref<Record<string, string>>({})
let justSaved: string[] = []
watch(
  () => props.items,
  (next, prev) => {
    drafts.value = rebase(drafts.value, prev ?? [], next, justSaved)
    justSaved = []
  },
  { immediate: true },
)

const touched = ref<string[]>([])
const attempted = ref(false)
const serverErrors = ref<Record<string, string>>({})
const busy = ref(false)
const error = ref('')
const detail = ref('')

const dirty = computed(() => dirtyKeys(props.items, drafts.value))
const found = computed(() => problems(props.items, drafts.value))
const canEdit = computed(() => props.items.some((i) => i.editable))
const locked = computed(() => props.items.some((i) => !i.editable))

const titleId = computed(() => `settings-${props.group}-title`)
const text = (key: string, fallback = '') => (te(key) ? t(key) : fallback)
const title = computed(() => text(`settings.group.${props.group}.title`, props.group))
const desc = computed(() => text(`settings.group.${props.group}.desc`))
const note = computed(() => text(`settings.group.${props.group}.note`))
const lockNote = computed(() => (props.group === 'payments' ? t('settings.locked_payments') : t('settings.locked')))

function problemText(p: Problem): string {
  const params: Record<string, string> = {}
  for (const [k, v] of Object.entries(p.params ?? {})) params[k] = num(v, lang.value)
  return t(`settings.invalid.${p.code}`, params)
}

// A problem shows once the field (or one it depends on) was left, or on Save.
function problemOf(key: string): string {
  const p = found.value[key]
  if (!p) return ''
  const seen = [key, ...(dependsOn[key] ?? [])].some((k) => touched.value.includes(k))
  return attempted.value || seen ? problemText(p) : ''
}

const channel = computed(() => {
  const c = props.items.find((i) => i.kind === 'channel')
  return c ? canonical(c, drafts.value[c.key] ?? '') : ''
})
function placeholderOf(item: SettingItem): string {
  return example(item, channel.value) || (item.kind === 'int' ? t('settings.not_set') : '')
}
function checkable(item: SettingItem): boolean {
  if (item.kind !== 'channel') return false
  const v = normalize(item, drafts.value[item.key] ?? '')
  return v !== '' && validate(item, v) === null
}

function update(key: string, v: string) {
  drafts.value = { ...drafts.value, [key]: v }
  if (serverErrors.value[key]) {
    const rest = { ...serverErrors.value }
    delete rest[key]
    serverErrors.value = rest
  }
  error.value = ''
  detail.value = ''
}
function touch(key: string) {
  if (!touched.value.includes(key)) touched.value = [...touched.value, key]
}

function reset() {
  drafts.value = rebase({}, [], props.items)
  touched.value = []
  attempted.value = false
  serverErrors.value = {}
  error.value = ''
  detail.value = ''
}

async function save() {
  if (!dirty.value.length || busy.value) return
  attempted.value = true
  const bad = props.items.find((i) => found.value[i.key])
  if (bad) {
    error.value = t('settings.fix_errors')
    detail.value = ''
    await nextTick()
    document.getElementById(fieldId(bad.key))?.focus()
    return
  }
  const values = changes(props.items, drafts.value)
  const maintenance = props.items.find((i) => i.key === 'maintenance.enabled')
  if (values['maintenance.enabled'] === 'true' && maintenance && effective(maintenance) !== 'true') {
    confirm.require({
      header: t('settings.maintenance_confirm.title'),
      message: t('settings.maintenance_confirm.message'),
      icon: 'pi pi-exclamation-triangle',
      acceptProps: { label: t('settings.maintenance_confirm.accept'), severity: 'warn' },
      rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
      accept: () => void send(values),
    })
    return
  }
  await send(values)
}

async function send(values: Record<string, string>) {
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    const reply = await api<SettingsReply>('PUT', '/settings', { values })
    justSaved = Object.keys(values)
    touched.value = []
    attempted.value = false
    serverErrors.value = {}
    emit('saved', reply)
    toast.add({ severity: 'success', summary: t('settings.saved'), detail: t('settings.saved_detail'), life: 4000 })
  } catch (e) {
    const err = errorText(e, t)
    const field = serverField(e)
    error.value = err.text
    if (field && props.items.some((i) => i.key === field)) {
      serverErrors.value = { ...serverErrors.value, [field]: err.detail || err.text }
    } else {
      detail.value = err.detail
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Card class="settings-card">
    <template #title>
      <h2 :id="titleId" class="card-title">{{ title }}</h2>
    </template>
    <template v-if="desc" #subtitle>{{ desc }}</template>
    <template #content>
      <Message v-if="locked" severity="secondary" icon="pi pi-lock" :closable="false" class="note">{{ lockNote }}</Message>
      <Message v-if="note" severity="info" :closable="false" class="note">{{ note }}</Message>
      <form class="settings-form" :aria-labelledby="titleId" novalidate @submit.prevent="save">
        <div class="fields">
          <SettingField
            v-for="item in items"
            :key="item.key"
            :item="item"
            :model-value="drafts[item.key] ?? ''"
            :problem="problemOf(item.key)"
            :server-error="serverErrors[item.key] ?? ''"
            :placeholder="placeholderOf(item)"
            :checkable="checkable(item)"
            @update:model-value="(v: string) => update(item.key, v)"
            @touch="touch(item.key)"
          />
        </div>
        <Message v-if="error" severity="error" :closable="false" class="error">
          {{ error }}
          <div v-if="detail" class="app-ltr detail">{{ detail }}</div>
        </Message>
        <div v-if="canEdit" class="actions">
          <span v-if="dirty.length" class="app-muted unsaved" aria-live="polite">{{ t('settings.unsaved') }}</span>
          <Button
            type="button"
            :label="t('settings.reset')"
            icon="pi pi-undo"
            severity="secondary"
            text
            :disabled="!dirty.length || busy"
            @click="reset"
          />
          <Button type="submit" :label="t('common.save')" icon="pi pi-check" :loading="busy" :disabled="!dirty.length" />
        </div>
      </form>
    </template>
  </Card>
</template>

<style scoped>
.card-title {
  margin: 0;
  font-size: 1.1rem;
  font-weight: 700;
}
.note {
  margin-block-end: 1rem;
}
/* Cards in a row share a height: Save stays at the bottom. */
.settings-card :deep(.p-card-body) {
  flex: 1 1 auto;
}
.settings-card :deep(.p-card-content) {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
}
.settings-form {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
}
.fields {
  display: grid;
  gap: 1.15rem;
}
.wide .fields {
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 18rem), 1fr));
  column-gap: 1.5rem;
}
.fields,
.error {
  margin-block-end: 1.25rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 0.5rem;
  margin-block-start: auto;
  padding-block-start: 1rem;
  border-block-start: 1px solid var(--p-content-border-color);
}
.unsaved {
  margin-inline-end: auto;
  font-size: 0.85rem;
}
</style>
