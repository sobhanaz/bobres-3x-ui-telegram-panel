<script setup lang="ts">
import { computed, ref } from 'vue'
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
import { api } from '../../api'
import { errorText } from '../../errors'
import { asciiNumber, isolate, num, type Lang } from '../../format'
import type { PlanGroupItem } from '../../types'

// Plan groups: a named group of plans with a text. In the bot (and the Mini App) a customer first picks a group, reads its
// text, then picks a plan of it; plans with no group are listed under "Other plans". A store with no group lists its plans
// as before. Which group a plan belongs to is chosen in the plan's editor.
const props = defineProps<{ groups: PlanGroupItem[] }>()
const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()

interface Form {
  id: string
  name_fa: string
  name_en: string
  text_fa: string
  text_en: string
  sort: string
}
const blank = (): Form => ({ id: '', name_fa: '', name_en: '', text_fa: '', text_en: '', sort: '0' })
const form = ref<Form>(blank())
const editing = ref(false)
const busy = ref(false)
const formError = ref('')

function edit(g?: PlanGroupItem) {
  formError.value = ''
  form.value = g
    ? { id: g.id, name_fa: g.name.fa ?? '', name_en: g.name.en ?? '', text_fa: g.description.fa ?? '', text_en: g.description.en ?? '', sort: String(g.sort) }
    : blank()
  editing.value = true
}

const valid = computed(() => !!(form.value.name_fa.trim() || form.value.name_en.trim()) && /^-?\d{0,6}$/.test(asciiNumber(form.value.sort)))

async function save() {
  const f = form.value
  busy.value = true
  formError.value = ''
  const body = {
    name: { fa: f.name_fa.trim(), en: f.name_en.trim() },
    description: { fa: f.text_fa.trim(), en: f.text_en.trim() },
    sort: Number(asciiNumber(f.sort)) || 0,
  }
  try {
    await api(f.id ? 'PUT' : 'POST', f.id ? `/plan-groups/${f.id}` : '/plan-groups', body)
    editing.value = false
    toast.add({ severity: 'success', summary: t('plan.group_saved'), life: 3000 })
    emit('changed')
  } catch (e) {
    formError.value = errorText(e, t).text
  } finally {
    busy.value = false
  }
}

const label = (g: PlanGroupItem) => g.name[lang.value] || g.name.en || g.name.fa || ''

function remove(g: PlanGroupItem) {
  confirm.require({
    header: t('plan.group_delete'),
    message: t('plan.group_delete_confirm', { name: isolate(label(g)) }),
    acceptProps: { label: t('plan.group_delete'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      try {
        await api('DELETE', `/plan-groups/${g.id}`)
        toast.add({ severity: 'success', summary: t('plan.group_deleted'), life: 3000 })
      } catch (e) {
        toast.add({ severity: 'error', summary: errorText(e, t).text, life: 6000 })
      }
      emit('changed')
    },
  })
}
</script>

<template>
  <section class="groups">
    <div class="app-page-head head">
      <h2 class="title">{{ t('plan.groups_title') }}</h2>
      <Button :label="t('plan.group_new')" icon="pi pi-plus" size="small" outlined @click="edit()" />
    </div>
    <p class="app-muted intro">{{ t('plan.groups_intro') }}</p>
    <DataTable v-if="props.groups.length" :value="props.groups" data-key="id" size="small" scrollable row-hover>
      <Column :header="t('plan.group_name')">
        <template #body="{ data: g }">
          <div><bdi>{{ label(g) }}</bdi></div>
          <div v-if="g.description.fa || g.description.en" class="app-muted text"><bdi>{{ g.description[lang] || g.description.en || g.description.fa }}</bdi></div>
        </template>
      </Column>
      <Column :header="t('plan.group_plans')">
        <template #body="{ data: g }">{{ num(g.plans, lang) }}</template>
      </Column>
      <Column :header="t('plan.sort')">
        <template #body="{ data: g }">{{ num(g.sort, lang) }}</template>
      </Column>
      <Column>
        <template #body="{ data: g }">
          <Button icon="pi pi-pencil" text rounded :aria-label="t('common.edit')" @click="edit(g)" />
          <Button icon="pi pi-trash" text rounded severity="danger" :aria-label="t('plan.group_delete')" @click="remove(g)" />
        </template>
      </Column>
    </DataTable>
    <p v-else class="app-muted">{{ t('plan.groups_none') }}</p>

    <Dialog v-model:visible="editing" :header="form.id ? t('plan.group_edit') : t('plan.group_new')" modal :style="{ inlineSize: 'min(36rem, 94vw)' }" :draggable="false">
      <form class="app-form" @submit.prevent="save">
        <div class="app-row">
          <label>
            <span>{{ t('plan.group_name_fa') }}</span>
            <InputText v-model="form.name_fa" dir="rtl" maxlength="60" />
          </label>
          <label>
            <span>{{ t('plan.group_name_en') }}</span>
            <InputText v-model="form.name_en" dir="ltr" maxlength="60" />
          </label>
        </div>
        <label>
          <span>{{ t('plan.group_text_fa') }}</span>
          <Textarea v-model="form.text_fa" dir="rtl" rows="5" maxlength="1500" auto-resize />
        </label>
        <label>
          <span>{{ t('plan.group_text_en') }}</span>
          <Textarea v-model="form.text_en" dir="ltr" rows="5" maxlength="1500" auto-resize />
        </label>
        <small class="app-muted">{{ t('plan.group_text_help') }}</small>
        <label>
          <span>{{ t('plan.sort') }}</span>
          <InputText v-model="form.sort" inputmode="numeric" dir="ltr" />
        </label>
        <Message v-if="formError" severity="error" :closable="false">{{ formError }}</Message>
        <div class="buttons">
          <Button type="button" :label="t('app.cancel')" severity="secondary" outlined @click="editing = false" />
          <Button type="submit" :label="t('common.save')" :loading="busy" :disabled="!valid" />
        </div>
      </form>
    </Dialog>
  </section>
</template>

<style scoped>
.groups {
  margin-block-end: 1.5rem;
}
.head {
  margin-block-end: 0.25rem;
}
.title {
  font-size: 1.1rem;
  margin: 0;
}
.intro {
  margin-block: 0 var(--app-gap);
}
.text {
  max-inline-size: 36rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.buttons {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
</style>
