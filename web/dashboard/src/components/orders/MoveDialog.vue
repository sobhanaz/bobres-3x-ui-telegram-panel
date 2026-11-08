<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Listbox from 'primevue/listbox'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import ReasonDialog from '../ReasonDialog.vue'
import { api } from '../../api'
import { errorText } from '../../errors'
import { isolate } from '../../format'
import { moveChoices, moveSeverity, moveState, type MoveState } from '../../undelivered'
import type { MoveResult, OrderItem, UndeliveredServer } from '../../types'

// "Deliver on another server": an undelivered new service is made on the server staff pick instead of the plan's,
// and tried at once (POST /orders/{id}/move {server_id, reason}). The plan and the price stay as they were. The
// servers are the enabled ones with their last health check; the caller reads them (GET /orders/undelivered).
const props = withDefaults(
  defineProps<{ order: OrderItem | null; servers: UndeliveredServer[]; serversError?: string; serversLoading?: boolean }>(),
  { serversError: '', serversLoading: false },
)
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ moved: [res: MoveResult] }>()
const { t } = useI18n()
const toast = useToast()
const labelId = useId()

const picked = ref<string | null>(null)
const busy = ref(false)
const error = ref('')
const detail = ref('')
watch(visible, (v) => {
  if (!v) return
  // the server the order was moved to before, while it is still offered
  picked.value = props.servers.some((s) => s.id === props.order?.server_id) ? (props.order?.server_id ?? null) : null
  error.value = ''
  detail.value = ''
})

const choices = computed(() => moveChoices(props.servers))
const chosen = computed(() => props.servers.find((s) => s.id === picked.value) ?? null)
const chosenState = computed<MoveState | null>(() => (chosen.value ? moveState(chosen.value) : null))
const stateLabel = (s: MoveState) => (s === 'full' ? t('order.move_state_full') : t('server.health_state.' + s))

async function confirm(reason: string) {
  if (!props.order || !chosen.value) return
  const server = chosen.value
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    const res = await api<MoveResult>('POST', `/orders/${props.order.id}/move`, { server_id: server.id, reason })
    visible.value = false
    const summary = t('order.move_done', { server: isolate(server.name) })
    // a half-made client may be left on the old server's panel: a warning that stays until it is closed
    if (res.leftover || res.warning) toast.add({ severity: 'warn', summary, detail: res.leftover ? t('order.move_leftover') : res.warning })
    else toast.add({ severity: 'success', summary, life: 5000 })
    emit('moved', res)
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
    :title="t('order.move')"
    :message="t('order.move_intro')"
    :confirm-label="t('order.move_confirm')"
    :busy="busy"
    :error="error"
    :detail="detail"
    :can-confirm="!!chosen"
    @confirm="confirm"
  >
    <Message v-if="serversError" severity="warn" :closable="false" class="servers-error">
      {{ t('order.move_servers_error') }}
      <div class="app-ltr detail">{{ serversError }}</div>
    </Message>
    <div class="field">
      <span :id="labelId">{{ t('order.move_server') }}</span>
      <Listbox
        v-model="picked"
        :options="choices"
        option-label="name"
        option-value="id"
        :empty-message="serversLoading ? t('app.loading') : t('order.move_none')"
        :aria-labelledby="labelId"
        list-style="max-block-size: 14rem"
        class="servers"
      >
        <template #option="{ option }">
          <span class="server">
            <bdi class="name">{{ option.name }}</bdi>
            <Tag :severity="moveSeverity[moveState(option)]" :value="stateLabel(moveState(option))" class="state" />
          </span>
        </template>
      </Listbox>
    </div>
    <Message v-if="chosen && chosenState !== 'ok'" :severity="chosenState === 'unknown' ? 'info' : 'warn'" :closable="false" class="server-warn">
      {{ t('order.move_warn.' + chosenState) }}
      <div v-if="chosenState === 'down' && chosen.error" class="app-ltr detail">{{ chosen.error }}</div>
    </Message>
  </ReasonDialog>
</template>

<style scoped>
.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.servers {
  inline-size: 100%;
}
.server {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  inline-size: 100%;
}
.name {
  overflow-wrap: anywhere;
}
.state {
  flex: none;
}
.servers-error,
.server-warn {
  margin-block: 0.5rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
</style>
