<script setup lang="ts">
import { computed, onMounted, reactive, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Textarea from 'primevue/textarea'
import Message from 'primevue/message'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { asciiNumber, isolate, num, type Lang } from '../../format'
import { CANNED_TITLE_MAX, REPLY_MAX, plainLength } from '../../support'
import type { CannedReply } from '../../types'

// Saved replies (tickets.manage): titles and texts in both languages that
// staff insert into a reply. The texts are the store's data, edited raw.
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()
const uid = useId()

const items = ref<CannedReply[]>([])
const loading = ref(true)
const loadError = ref('')
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    items.value = (await api<{ items: CannedReply[] }>('GET', '/canned-replies')).items
  } catch (e) {
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(load)

interface Form {
  title: string
  fa: string
  en: string
  sort: string
}
const blank = (): Form => ({ title: '', fa: '', en: '', sort: '' })
const form = reactive<Form>(blank())
const editing = ref<CannedReply | null>(null)
const open = ref(false)
const busy = ref(false)
const formError = ref('')
const formDetail = ref('')
const fieldError = ref('')

function edit(c?: CannedReply) {
  editing.value = c ?? null
  Object.assign(form, c ? { title: c.title, fa: c.body.fa ?? '', en: c.body.en ?? '', sort: String(c.sort) } : blank())
  formError.value = ''
  formDetail.value = ''
  fieldError.value = ''
  open.value = true
}

const sortValue = computed(() => asciiNumber(form.sort))
const sortOk = computed(() => sortValue.value === '' || /^-?\d{1,6}$/.test(sortValue.value))
const titleOk = computed(() => form.title.trim().length > 0 && form.title.trim().length <= CANNED_TITLE_MAX)
const lenOk = (s: string) => plainLength(s) <= REPLY_MAX
const valid = computed(
  () => titleOk.value && sortOk.value && (form.fa.trim() !== '' || form.en.trim() !== '') && lenOk(form.fa) && lenOk(form.en),
)

async function save() {
  if (!valid.value) return
  busy.value = true
  formError.value = ''
  formDetail.value = ''
  fieldError.value = ''
  const body = {
    title: form.title.trim(),
    body: { fa: form.fa.trim(), en: form.en.trim() },
    sort: sortValue.value === '' ? 0 : Number(sortValue.value),
  }
  try {
    if (editing.value) await api('PUT', `/canned-replies/${editing.value.id}`, body)
    else await api('POST', '/canned-replies', body)
    open.value = false
    toast.add({ severity: 'success', summary: t('support.canned.saved'), life: 3000 })
    await load()
  } catch (e) {
    const err = errorText(e, t)
    formError.value = err.text
    formDetail.value = err.detail
    if (e instanceof ApiError && typeof e.data.field === 'string') fieldError.value = e.data.field
  } finally {
    busy.value = false
  }
}

function remove(c: CannedReply) {
  confirm.require({
    header: t('support.canned.delete'),
    message: t('support.canned.delete_confirm', { title: isolate(c.title) }),
    acceptProps: { label: t('support.canned.delete'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      try {
        await api('DELETE', `/canned-replies/${c.id}`)
        items.value = items.value.filter((x) => x.id !== c.id)
        toast.add({ severity: 'success', summary: t('support.canned.deleted'), life: 3000 })
      } catch (e) {
        const err = errorText(e, t)
        toast.add({ severity: 'error', summary: err.text, detail: err.detail || undefined, life: 6000 })
      }
    },
  })
}

const firstLine = (s: string) => (s ?? '').trim().split('\n')[0]
const fieldId = (name: string) => `${uid}-${name}`
</script>

<template>
  <div class="head">
    <p class="app-muted hint">{{ t('support.canned.hint') }}</p>
    <Button :label="t('support.canned.new')" icon="pi pi-plus" @click="edit()" />
  </div>
  <Message v-if="loadError" severity="error" :closable="false">
    {{ loadError }}
    <Button :label="t('app.retry')" text size="small" @click="load" />
  </Message>
  <DataTable :value="items" :loading="loading" data-key="id" size="small" scrollable class="canned">
    <template #empty>{{ t('support.canned.none') }}</template>
    <Column :header="t('support.canned.title_label')">
      <template #body="{ data: c }"><bdi class="title">{{ c.title }}</bdi></template>
    </Column>
    <Column :header="t('support.canned.fa')">
      <template #body="{ data: c }">
        <span v-if="c.body.fa" class="line" dir="rtl" lang="fa">{{ firstLine(c.body.fa) }}</span>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
    <Column :header="t('support.canned.en')">
      <template #body="{ data: c }">
        <span v-if="c.body.en" class="line" dir="ltr" lang="en">{{ firstLine(c.body.en) }}</span>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
    <Column :header="t('support.canned.sort')" body-class="app-nowrap">
      <template #body="{ data: c }">{{ num(c.sort, lang) }}</template>
    </Column>
    <Column body-class="app-nowrap">
      <template #body="{ data: c }">
        <Button icon="pi pi-pencil" text rounded size="small" :aria-label="t('support.canned.edit', { title: c.title })" v-tooltip.top="t('common.edit')" @click="edit(c)" />
        <Button icon="pi pi-trash" text rounded size="small" severity="danger" :aria-label="t('support.canned.delete_one', { title: c.title })" v-tooltip.top="t('support.canned.delete')" @click="remove(c)" />
      </template>
    </Column>
  </DataTable>

  <Dialog
    v-model:visible="open"
    :header="editing ? t('support.canned.edit_title') : t('support.canned.new')"
    modal
    :draggable="false"
    :style="{ inlineSize: 'min(48rem, 96vw)' }"
  >
    <form class="app-form" @submit.prevent="save">
      <div class="app-row">
        <label :for="fieldId('title')">
          <span>{{ t('support.canned.title_label') }}</span>
          <InputText :id="fieldId('title')" v-model="form.title" :maxlength="CANNED_TITLE_MAX" :invalid="fieldError === 'title'" autocomplete="off" />
          <small class="app-muted">{{ t('support.canned.title_hint') }}</small>
        </label>
        <label :for="fieldId('sort')">
          <span>{{ t('support.canned.sort') }}</span>
          <InputText :id="fieldId('sort')" v-model="form.sort" inputmode="numeric" dir="ltr" autocomplete="off" :invalid="!sortOk || fieldError === 'sort'" />
          <small class="app-muted">{{ t('support.canned.sort_hint') }}</small>
        </label>
      </div>
      <div class="langs">
        <div v-for="l in (['fa', 'en'] as const)" :key="l" class="app-field">
          <div class="lang-head">
            <label :for="fieldId(l)">{{ t('support.canned.' + l) }}</label>
            <span class="counter" :class="{ over: !lenOk(form[l]) }">{{ num(plainLength(form[l]), lang) }} / {{ num(REPLY_MAX, lang) }}</span>
          </div>
          <Textarea
            :id="fieldId(l)"
            v-model="form[l]"
            :dir="l === 'fa' ? 'rtl' : 'ltr'"
            :lang="l"
            rows="5"
            auto-resize
            fluid
            :invalid="!lenOk(form[l]) || fieldError === 'body.' + l"
          />
        </div>
      </div>
      <small class="app-muted">{{ t('support.canned.langs_hint') }}</small>
      <Message v-if="formError" severity="error" :closable="false">
        {{ formError }}
        <div v-if="formDetail" class="app-ltr detail">{{ formDetail }}</div>
      </Message>
    </form>
    <template #footer>
      <Button :label="t('app.cancel')" severity="secondary" outlined @click="open = false" />
      <Button :label="t('common.save')" icon="pi pi-check" :disabled="!valid" :loading="busy" @click="save" />
    </template>
  </Dialog>
</template>

<style scoped>
.head {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  align-items: center;
  justify-content: space-between;
  margin-block-end: 0.75rem;
}
.hint {
  margin: 0;
  font-size: 0.9rem;
  flex: 1 1 16rem;
}
.title {
  font-weight: 500;
}
.line {
  display: block;
  max-inline-size: 22rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.langs {
  display: grid;
  gap: 1rem;
  grid-template-columns: repeat(auto-fit, minmax(min(18rem, 100%), 1fr));
}
.lang-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}
.counter {
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
  font-variant-numeric: tabular-nums;
}
.counter.over {
  color: var(--p-red-500);
  font-weight: 700;
}
.detail {
  font-size: 0.85rem;
}
</style>
