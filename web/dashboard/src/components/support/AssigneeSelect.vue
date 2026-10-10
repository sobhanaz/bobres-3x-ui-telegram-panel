<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Select from 'primevue/select'
import { api } from '../../api'
import { errorText } from '../../errors'
import { useAuth } from '../../stores/auth'
import { isolate } from '../../format'
import { assigneeChoices, staffName, type Assignee } from '../../support'
import type { TicketItem, UserBrief } from '../../types'

// Who handles the ticket. Anyone may take it or let it go; giving it to
// someone else needs tickets.manage (canManage from core).
const props = defineProps<{ ticketId: string; current: UserBrief | null; canManage: boolean; labelledBy?: string }>()
const emit = defineEmits<{ changed: [ticket: TicketItem] }>()
const { t } = useI18n()
const toast = useToast()
const auth = useAuth()

const staff = ref<Assignee[]>([])
onMounted(async () => {
  try {
    staff.value = (await api<{ items: Assignee[] }>('GET', '/tickets/assignees')).items
  } catch {
    staff.value = [] // me and nobody still work
  }
})

const me = computed<UserBrief | null>(() => {
  const u = auth.me?.user
  return u ? { id: u.id, telegram_id: u.telegram_id, username: u.username } : null
})
const options = computed(() =>
  assigneeChoices(staff.value, me.value, props.canManage, props.current).map((c) => ({
    value: c.id,
    disabled: c.disabled,
    label: c.id === '' ? t('support.nobody') : c.me ? t('support.me', { name: isolate(staffName(c.user)) }) : staffName(c.user),
    role: c.user && 'role' in c.user ? String((c.user as Assignee).role) : '',
  })),
)

// A refused change puts the picker back (a new one replaces it: PrimeVue
// keeps its own copy of the last choice).
const pickerKey = ref(0)
const busy = ref(false)
async function pick(value: string | null) {
  const id = value ?? ''
  if (busy.value || id === (props.current?.id ?? '')) return
  busy.value = true
  try {
    const res = await api<{ ticket: TicketItem }>('PUT', `/tickets/${props.ticketId}/assignee`, { user_id: id || null })
    emit('changed', res.ticket)
    toast.add({ severity: 'success', summary: id ? t('support.assigned_done') : t('support.unassigned_done'), life: 3000 })
  } catch (e) {
    const err = errorText(e, t)
    toast.add({ severity: 'error', summary: err.text, detail: err.detail || undefined, life: 6000 })
    pickerKey.value++
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Select
    :key="pickerKey"
    :model-value="current?.id ?? ''"
    :options="options"
    option-label="label"
    option-value="value"
    option-disabled="disabled"
    :placeholder="t('support.nobody')"
    :loading="busy"
    :disabled="busy"
    :aria-label="labelledBy ? undefined : t('support.assignee')"
    :aria-labelledby="labelledBy"
    class="assignee"
    fluid
    @update:model-value="pick"
  >
    <template #option="{ option }">
      <span class="opt">
        <bdi>{{ option.label }}</bdi>
        <small v-if="option.role" class="app-muted">{{ t('role.' + option.role) }}</small>
      </span>
    </template>
  </Select>
</template>

<style scoped>
.opt {
  display: inline-flex;
  gap: 0.5rem;
  align-items: baseline;
}
</style>
