<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import Message from 'primevue/message'
import SessionList from './SessionList.vue'
import ReasonDialog from '../ReasonDialog.vue'
import { api } from '../../api'
import { errorText } from '../../errors'
import { isolate, num, type Lang } from '../../format'
import type { SessionItem, StaffItem } from '../../types'

// A staff member's logged-in devices: end one, or all of them with a reason.
const props = defineProps<{ staff: StaffItem | null }>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()

const name = computed(() => (props.staff ? isolate(props.staff.username ? '@' + props.staff.username : String(props.staff.telegram_id)) : ''))
const items = ref<SessionItem[]>([])
const loading = ref(false)
const error = ref('')
const busy = ref('')

async function load() {
  if (!props.staff) return
  const id = props.staff.id
  loading.value = true
  error.value = ''
  try {
    const res = await api<{ items: SessionItem[] }>('GET', `/staff/${id}/sessions`)
    if (props.staff?.id === id) items.value = res.items
  } catch (e) {
    error.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
watch(visible, (v) => {
  if (!v) return
  items.value = []
  void load()
})

async function revoke(s: SessionItem) {
  if (!props.staff) return
  busy.value = s.id
  error.value = ''
  try {
    await api('POST', `/staff/${props.staff.id}/sessions/${s.id}/revoke`, {})
    toast.add({ severity: 'success', summary: t('staff.revoked_one'), life: 3000 })
    emit('changed')
    await load()
  } catch (e) {
    error.value = errorText(e, t).text
  } finally {
    busy.value = ''
  }
}

// Log out everywhere: a reason, kept in the audit log.
const allOpen = ref(false)
const allBusy = ref(false)
const allError = ref('')
const allDetail = ref('')
function openAll() {
  allError.value = ''
  allDetail.value = ''
  allOpen.value = true
}
async function revokeAll(reason: string) {
  if (!props.staff) return
  allBusy.value = true
  allError.value = ''
  try {
    const res = await api<{ revoked: number }>('POST', `/staff/${props.staff.id}/sessions/revoke`, { reason })
    allOpen.value = false
    toast.add({ severity: 'success', summary: t('staff.revoked_all', { n: num(res.revoked, lang.value) }), life: 4000 })
    emit('changed')
    await load()
  } catch (e) {
    const err = errorText(e, t)
    allError.value = err.text
    allDetail.value = err.detail
  } finally {
    allBusy.value = false
  }
}
</script>

<template>
  <Dialog v-model:visible="visible" :header="t('staff.sessions_title', { name })" modal :style="{ inlineSize: 'min(36rem, 94vw)' }" :draggable="false">
    <Message v-if="error" severity="error" :closable="false" class="msg">{{ error }}</Message>
    <SessionList :items="items" :loading="loading" :busy="busy" :empty="t('staff.sessions_none')" @revoke="revoke" />
    <template #footer>
      <Button :label="t('staff.close')" severity="secondary" outlined @click="visible = false" />
      <Button v-if="items.length" :label="t('staff.revoke_all')" icon="pi pi-sign-out" severity="danger" outlined @click="openAll" />
    </template>
  </Dialog>

  <ReasonDialog
    v-model:visible="allOpen"
    :title="t('staff.revoke_all')"
    :message="t('staff.revoke_all_intro', { name })"
    :confirm-label="t('staff.revoke_all')"
    danger
    :busy="allBusy"
    :error="allError"
    :detail="allDetail"
    @confirm="revokeAll"
  />
</template>

<style scoped>
.msg {
  margin-block-end: 0.75rem;
}
</style>
