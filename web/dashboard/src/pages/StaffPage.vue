<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Menu from 'primevue/menu'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import type { MenuItem } from 'primevue/menuitem'
import UserLink from '../components/UserLink.vue'
import StatusTag from '../components/StatusTag.vue'
import ReasonDialog from '../components/ReasonDialog.vue'
import AddStaffDialog from '../components/staff/AddStaffDialog.vue'
import StaffSessionsDialog from '../components/staff/StaffSessionsDialog.vue'
import { api } from '../api'
import { errorText } from '../errors'
import { dateTime, isolate, num, type Lang } from '../format'
import { loginState, roleOptions, staffActions, type StaffAction, type StaffRole } from '../staff'
import type { StaffItem } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const router = useRouter()
const toast = useToast()

const items = ref<StaffItem[]>([])
const ownerConfigured = ref(true)
const canGrantOwner = ref(false)
const loading = ref(true)
const loadError = ref('')
const now = ref(Date.now() / 1000)
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const res = await api<{ items: StaffItem[]; owner_configured: boolean; can_grant_owner: boolean }>('GET', '/staff')
    items.value = res.items
    ownerConfigured.value = res.owner_configured
    canGrantOwner.value = res.can_grant_owner
    now.value = Date.now() / 1000
  } catch (e) {
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(load)

const nameOf = (s: StaffItem) => isolate(s.username ? '@' + s.username : String(s.telegram_id))
const roleSeverity: Record<string, 'contrast' | 'info' | 'secondary'> = { owner: 'contrast', admin: 'info', support: 'secondary' }
const loginSeverity = { off: 'secondary', pending: 'warn', on: 'success', locked: 'danger' } as const

// The row menu: what the viewer may do, then the row's audit history.
const menu = ref<InstanceType<typeof Menu> | null>(null)
const menuItems = ref<MenuItem[]>([])
const target = ref<StaffItem | null>(null)
function openMenu(event: Event, s: StaffItem) {
  target.value = s
  const allowed = staffActions(s, canGrantOwner.value, Date.now() / 1000)
  const labels: Record<StaffAction, { label: string; icon: string; danger?: boolean }> = {
    role: { label: t('staff.change_role'), icon: 'pi pi-id-card' },
    sessions: { label: t('staff.sessions'), icon: 'pi pi-desktop' },
    reset: { label: t('staff.reset'), icon: 'pi pi-key', danger: true },
    unlock: { label: t('staff.unlock'), icon: 'pi pi-lock-open' },
    remove: { label: t('staff.remove'), icon: 'pi pi-user-minus', danger: true },
  }
  const actions: MenuItem[] = allowed.map((a) => ({
    label: labels[a].label,
    icon: labels[a].icon,
    class: labels[a].danger ? 'staff-danger' : undefined,
    command: () => start(a, s),
  }))
  const history: MenuItem[] = [
    { label: t('staff.their_actions'), icon: 'pi pi-history', command: () => router.push({ name: 'audit', query: { actor: s.id } }) },
    { label: t('staff.changes_to_them'), icon: 'pi pi-list', command: () => router.push({ name: 'audit', query: { entity_id: s.id } }) },
  ]
  menuItems.value = actions.length ? [...actions, { separator: true }, ...history] : history
  menu.value?.toggle(event)
}

// One action dialog at a time (sessions has its own).
const action = ref<'role' | 'reset' | 'remove' | null>(null)
const actionOpen = computed({ get: () => action.value !== null, set: (v) => !v && (action.value = null) })
const busy = ref(false)
const actionError = ref('')
const actionDetail = ref('')
const newRole = ref<StaffRole>('support')
const sessionsOpen = ref(false)

function start(a: StaffAction, s: StaffItem) {
  target.value = s
  actionError.value = ''
  actionDetail.value = ''
  if (a === 'sessions') {
    sessionsOpen.value = true
  } else if (a === 'unlock') {
    void unlock(s)
  } else {
    newRole.value = s.role
    action.value = a
  }
}

const roleChoices = computed(() => roleOptions(canGrantOwner.value).map((r) => ({ value: r, label: t('role.' + r) })))
const dialog = computed(() => {
  const name = target.value ? nameOf(target.value) : ''
  switch (action.value) {
    case 'role':
      return { title: t('staff.change_role'), message: t('staff.role_intro', { name }), confirm: t('staff.change_role'), danger: false }
    case 'reset':
      return { title: t('staff.reset'), message: t('staff.reset_intro', { name }), confirm: t('staff.reset'), danger: true }
    case 'remove':
      return { title: t('staff.remove'), message: t('staff.remove_intro', { name }), confirm: t('staff.remove'), danger: true }
  }
  return { title: '', message: '', confirm: '', danger: false }
})

async function confirmAction(reason: string) {
  const s = target.value
  if (!s || !action.value) return
  busy.value = true
  actionError.value = ''
  try {
    let done = ''
    switch (action.value) {
      case 'role':
        await api('PUT', `/staff/${s.id}/role`, { role: newRole.value, reason })
        done = t('staff.role_done', { name: nameOf(s), role: t('role.' + newRole.value) })
        break
      case 'reset':
        await api('POST', `/staff/${s.id}/password/reset`, { reason })
        done = t('staff.reset_done')
        break
      case 'remove':
        await api('POST', `/staff/${s.id}/remove`, { reason })
        done = t('staff.removed', { name: nameOf(s) })
        break
    }
    action.value = null
    toast.add({ severity: 'success', summary: done, life: 5000 })
    await load()
  } catch (e) {
    const err = errorText(e, t)
    actionError.value = err.text
    actionDetail.value = err.detail
  } finally {
    busy.value = false
  }
}

async function unlock(s: StaffItem) {
  try {
    await api('POST', `/staff/${s.id}/unlock`, {})
    toast.add({ severity: 'success', summary: t('staff.unlocked'), life: 4000 })
  } catch (e) {
    const err = errorText(e, t)
    toast.add({ severity: 'error', summary: err.text, detail: err.detail || undefined, life: 6000 })
  }
  await load()
}

const addOpen = ref(false)
function added(s: StaffItem) {
  toast.add({ severity: 'success', summary: t('staff.added', { name: nameOf(s), role: t('role.' + s.role) }), detail: t('staff.added_hint'), life: 8000 })
  void load()
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.staff.title') }}</h1>
    <Button :label="t('staff.add')" icon="pi pi-user-plus" @click="addOpen = true" />
  </div>
  <Message v-if="loadError" severity="error" :closable="false">{{ loadError }}</Message>
  <p class="app-muted intro">{{ t('staff.intro') }}</p>
  <Message v-if="!loading && !loadError && !ownerConfigured" severity="warn" :closable="false" class="note">{{ t('staff.no_owner') }}</Message>

  <DataTable :value="items" :loading="loading" data-key="id" size="small" scrollable>
    <template #empty>{{ t('staff.none') }}</template>
    <Column :header="t('staff.member')">
      <template #body="{ data: s }">
        <div class="who">
          <UserLink :user="s" />
          <Tag v-if="s.me" :value="t('staff.you')" severity="info" />
          <Tag v-if="s.configured_owner" :value="t('staff.store_owner')" severity="warn" icon="pi pi-shield" />
          <StatusTag v-if="s.status !== 'active'" kind="user" :status="s.status" />
        </div>
        <div v-if="s.configured_owner && !s.me" class="app-muted small">{{ t('staff.owner_note') }}</div>
      </template>
    </Column>
    <Column :header="t('staff.role')">
      <template #body="{ data: s }">
        <Tag :value="t('role.' + s.role)" :severity="roleSeverity[s.role] ?? 'secondary'" />
      </template>
    </Column>
    <Column :header="t('staff.login_col')">
      <template #body="{ data: s }">
        <Tag :value="t('staff.login.' + loginState(s, now))" :severity="loginSeverity[loginState(s, now)]" />
        <div v-if="loginState(s, now) === 'locked'" class="app-muted small app-nowrap">
          {{ t('staff.locked_until', { time: dateTime(s.password.locked_until!, lang) }) }}
        </div>
        <div v-else-if="s.password.state !== 'off' && s.password.failed_attempts > 0" class="app-muted small">
          {{ t('staff.failed_tries', { n: num(s.password.failed_attempts, lang) }) }}
        </div>
        <div v-if="s.password.username" class="app-muted small"><span class="app-ltr app-mono">{{ s.password.username }}</span></div>
      </template>
    </Column>
    <Column :header="t('staff.last_login')">
      <template #body="{ data: s }">
        <template v-if="s.last_login">
          <div class="app-nowrap">{{ dateTime(s.last_login.at, lang) }}</div>
          <div class="app-muted small">
            {{ t('account.method.' + s.last_login.method) }}
            <span v-if="s.last_login.ip" class="app-ltr">{{ s.last_login.ip }}</span>
          </div>
        </template>
        <span v-else class="app-muted">{{ t('staff.never') }}</span>
      </template>
    </Column>
    <Column :header="t('staff.sessions')">
      <template #body="{ data: s }">{{ num(s.sessions, lang) }}</template>
    </Column>
    <Column body-class="actions">
      <template #body="{ data: s }">
        <RouterLink v-if="s.me" :to="{ name: 'account' }" class="small">{{ t('staff.my_account') }}</RouterLink>
        <Button
          v-else
          icon="pi pi-ellipsis-v"
          text
          rounded
          severity="secondary"
          aria-haspopup="true"
          aria-controls="staff-menu"
          :aria-label="t('staff.actions', { name: nameOf(s) })"
          @click="openMenu($event, s)"
        />
      </template>
    </Column>
  </DataTable>
  <Menu id="staff-menu" ref="menu" :model="menuItems" popup />

  <ReasonDialog
    v-model:visible="actionOpen"
    :title="dialog.title"
    :message="dialog.message"
    :confirm-label="dialog.confirm"
    :danger="dialog.danger"
    :busy="busy"
    :error="actionError"
    :detail="actionDetail"
    :can-confirm="action !== 'role' || newRole !== target?.role"
    @confirm="confirmAction"
  >
    <div v-if="action === 'role'" class="app-form">
      <label>
        <span>{{ t('staff.new_role') }}</span>
        <Select v-model="newRole" :options="roleChoices" option-label="label" option-value="value" />
        <small v-if="target && newRole === target.role" class="app-muted">{{ t('staff.same_role') }}</small>
        <small v-else class="app-muted">{{ t('staff.role_desc.' + newRole) }}</small>
      </label>
    </div>
  </ReasonDialog>

  <StaffSessionsDialog v-model:visible="sessionsOpen" :staff="target" @changed="load" />
  <AddStaffDialog v-model:visible="addOpen" :can-grant-owner="canGrantOwner" @added="added" />
</template>

<style scoped>
.intro {
  margin-block: 0 var(--app-gap);
}
.note {
  margin-block-end: var(--app-gap);
}
.who {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.4rem;
}
.small {
  font-size: 0.82rem;
}
:deep(.actions) {
  text-align: end;
  white-space: nowrap;
}
</style>

<style>
/* Row-menu items that take something away (the menu is placed on <body>). */
.p-menu-item.staff-danger {
  --p-menu-item-color: var(--p-button-text-danger-color, var(--p-red-500));
  --p-menu-item-focus-color: var(--p-button-text-danger-color, var(--p-red-500));
  --p-menu-item-icon-color: var(--p-button-text-danger-color, var(--p-red-500));
  --p-menu-item-icon-focus-color: var(--p-button-text-danger-color, var(--p-red-500));
}
</style>
