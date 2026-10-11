<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import InputText from 'primevue/inputtext'
import IconField from 'primevue/iconfield'
import InputIcon from 'primevue/inputicon'
import Listbox from 'primevue/listbox'
import RadioButton from 'primevue/radiobutton'
import Message from 'primevue/message'
import ReasonDialog from '../ReasonDialog.vue'
import { api, query } from '../../api'
import { errorText } from '../../errors'
import { latinDigits } from '../../format'
import { addable, roleOptions, type StaffRole } from '../../staff'
import type { Page, StaffItem, UserItem } from '../../types'

// Add someone to the staff: find them among the bot's customers, pick a
// role, and give a reason. They must have opened the bot already.
const props = defineProps<{ canGrantOwner: boolean }>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ added: [staff: StaffItem] }>()
const { t } = useI18n()

const q = ref('')
const results = ref<UserItem[]>([])
const searching = ref(false)
const searchError = ref('')
const picked = ref<string | null>(null)
const role = ref<StaffRole>('support')
const busy = ref(false)
const error = ref('')
const detail = ref('')

watch(visible, (v) => {
  if (!v) return
  q.value = ''
  results.value = []
  picked.value = null
  role.value = 'support'
  error.value = ''
  detail.value = ''
  searchError.value = ''
})

// Search as they type, a moment after the last key; older answers are dropped.
let seq = 0
let timer: ReturnType<typeof setTimeout> | undefined
watch(q, (v) => {
  clearTimeout(timer)
  const mine = ++seq
  const text = latinDigits(v.trim())
  if (!text) {
    results.value = []
    searching.value = false
    return
  }
  searching.value = true
  timer = setTimeout(() => void search(text, mine), 300)
})
async function search(text: string, mine: number) {
  searchError.value = ''
  try {
    const res = await api<Page<UserItem>>('GET', '/users' + query({ q: text, role: 'user', status: 'active', size: 10 }))
    if (mine !== seq) return
    results.value = res.items.filter(addable)
    if (picked.value && !results.value.some((u) => u.id === picked.value)) picked.value = null
  } catch (e) {
    if (mine === seq) searchError.value = errorText(e, t).text
  } finally {
    if (mine === seq) searching.value = false
  }
}

const options = computed(() =>
  results.value.map((u) => ({ id: u.id, username: u.username, telegram_id: u.telegram_id, label: u.username ? '@' + u.username : String(u.telegram_id) })),
)
const roles = computed(() => roleOptions(props.canGrantOwner))
const empty = computed(() => (q.value.trim() ? (searching.value ? t('app.loading') : t('staff.search_none')) : t('staff.search_empty')))

async function add(reason: string) {
  if (!picked.value) return
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    const res = await api<{ staff: StaffItem }>('POST', '/staff', { user_id: picked.value, role: role.value, reason })
    visible.value = false
    emit('added', res.staff)
  } catch (e) {
    const err = errorText(e, t)
    error.value = err.text
    detail.value = err.detail
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <ReasonDialog
    v-model:visible="visible"
    :title="t('staff.add')"
    :message="t('staff.add_intro')"
    :confirm-label="t('staff.add_confirm')"
    :busy="busy"
    :error="error"
    :detail="detail"
    :can-confirm="!!picked"
    @confirm="add"
  >
    <div class="app-form">
      <label>
        <span>{{ t('staff.search') }}</span>
        <IconField>
          <InputIcon :class="searching ? 'pi pi-spin pi-spinner' : 'pi pi-search'" />
          <InputText v-model="q" fluid autocomplete="off" dir="auto" maxlength="64" />
        </IconField>
      </label>
      <Message v-if="searchError" severity="error" :closable="false">{{ searchError }}</Message>
      <Listbox
        v-model="picked"
        :options="options"
        option-label="label"
        option-value="id"
        :empty-message="empty"
        :aria-label="t('staff.person')"
        list-style="max-block-size: 12rem"
        class="people"
      >
        <template #option="{ option }">
          <span class="person">
            <bdi>{{ option.username ? '@' + option.username : '' }}</bdi>
            <span class="app-ltr app-muted">{{ option.telegram_id }}</span>
          </span>
        </template>
      </Listbox>

      <fieldset class="roles">
        <legend>{{ t('staff.role') }}</legend>
        <div v-for="r in roles" :key="r" class="role">
          <RadioButton v-model="role" :input-id="'add-role-' + r" name="add-role" :value="r" />
          <label :for="'add-role-' + r" class="role-text">
            <strong>{{ t('role.' + r) }}</strong>
            <small class="app-muted">{{ t('staff.role_desc.' + r) }}</small>
          </label>
        </div>
      </fieldset>
    </div>
  </ReasonDialog>
</template>

<style scoped>
.people {
  inline-size: 100%;
}
.person {
  display: inline-flex;
  gap: 0.5rem;
  align-items: baseline;
}
.roles {
  border: 0;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}
.roles legend {
  padding: 0;
  margin-block-end: 0.35rem;
}
.role {
  display: flex;
  align-items: flex-start;
  gap: 0.6rem;
}
.app-form .role-text {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
  cursor: pointer;
}
</style>
