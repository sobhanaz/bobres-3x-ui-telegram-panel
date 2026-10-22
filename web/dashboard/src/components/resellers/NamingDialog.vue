<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import { api } from '../../api'
import { errorText } from '../../errors'
import { configPrefix, PANEL_GROUP_PREFIX, panelGroupName, type Reseller } from '../../resellers'
import type { ResellerDetail } from '../../types'

// The owner's: what a reseller's configs are called and grouped as in the 3x-ui panel. The group is "BOBRES-NAME" (the owner writes
// NAME); the reseller chooses the prefix of the names once in the bot, and only the owner changes it after that. A new prefix is
// for the configs made after it; a new group name moves the earlier configs in the background (or "Move the existing configs" asks again).
const props = defineProps<{ reseller: Reseller }>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ saved: [item: ResellerDetail | undefined] }>()
const { t } = useI18n()
const toast = useToast()

const prefix = ref('')
const group = ref('')
const busy = ref(false)
const moving = ref(false)
const error = ref('')

watch(visible, (v) => {
  if (!v) return
  prefix.value = props.reseller.configPrefix
  group.value = props.reseller.panelGroup
  error.value = ''
})

// What typed text becomes ('' = nothing, or not allowed).
const wantedPrefix = computed(() => (prefix.value.trim() ? configPrefix(prefix.value) : ''))
const wantedGroup = computed(() => (group.value.trim() ? panelGroupName(group.value) : ''))
const prefixBad = computed(() => !!prefix.value.trim() && !wantedPrefix.value)
const groupBad = computed(() => !!group.value.trim() && !wantedGroup.value)
const changed = computed(() => wantedPrefix.value !== props.reseller.configPrefix || wantedGroup.value !== props.reseller.panelGroup)
const canSave = computed(() => changed.value && !prefixBad.value && !groupBad.value)

async function save() {
  busy.value = true
  error.value = ''
  const body: Record<string, string> = {}
  if (wantedPrefix.value !== props.reseller.configPrefix) body.config_prefix = wantedPrefix.value
  if (wantedGroup.value !== props.reseller.panelGroup) body.panel_group = wantedGroup.value
  try {
    const res = await api<{ item: ResellerDetail }>('PUT', `/resellers/${props.reseller.id}/naming`, body)
    visible.value = false
    toast.add({ severity: 'success', summary: t('reseller.naming_saved'), life: 4000 })
    emit('saved', res.item)
  } catch (e) {
    error.value = errorText(e, t).text
  } finally {
    busy.value = false
  }
}

async function moveExisting() {
  moving.value = true
  error.value = ''
  try {
    const res = await api<{ item: ResellerDetail }>('POST', `/resellers/${props.reseller.id}/group-sync`, {})
    visible.value = false
    toast.add({ severity: 'success', summary: t('reseller.naming_moving', { group: props.reseller.panelGroupFull }), life: 6000 })
    emit('saved', res.item)
  } catch (e) {
    error.value = errorText(e, t).text
  } finally {
    moving.value = false
  }
}
</script>

<template>
  <Dialog v-model:visible="visible" :header="t('reseller.naming_title')" modal :style="{ inlineSize: 'min(34rem, 94vw)' }" :draggable="false">
    <form class="app-form" @submit.prevent="save">
      <p class="app-muted">{{ t('reseller.naming_intro') }}</p>
      <label>
        <span>{{ t('reseller.panel_group') }}</span>
        <span class="group-row">
          <span class="app-ltr app-mono lead">{{ PANEL_GROUP_PREFIX }}</span>
          <InputText v-model="group" dir="ltr" maxlength="30" autocomplete="off" :invalid="groupBad" class="grow" />
        </span>
        <small :class="groupBad ? 'app-error' : 'app-muted'">{{ groupBad ? t('reseller.panel_group_rule') : t('reseller.panel_group_hint') }}</small>
      </label>
      <label>
        <span>{{ t('reseller.config_prefix') }}</span>
        <InputText v-model="prefix" dir="ltr" maxlength="10" autocomplete="off" :invalid="prefixBad" />
        <small :class="prefixBad ? 'app-error' : 'app-muted'">{{ prefixBad ? t('reseller.config_prefix_rule') : t('reseller.config_prefix_hint') }}</small>
      </label>
      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
      <div class="buttons">
        <Button
          v-if="reseller.panelGroup"
          type="button"
          :label="t('reseller.naming_move')"
          icon="pi pi-arrow-right-arrow-left"
          outlined
          :loading="moving"
          :disabled="busy"
          @click="moveExisting"
        />
        <span class="spacer" />
        <Button type="button" :label="t('app.cancel')" severity="secondary" outlined @click="visible = false" />
        <Button type="submit" :label="t('common.save')" :loading="busy" :disabled="!canSave" />
      </div>
    </form>
  </Dialog>
</template>

<style scoped>
.group-row {
  display: flex;
  align-items: center;
  gap: 0.35rem;
}
.lead {
  white-space: nowrap;
}
.grow {
  flex: 1;
}
.buttons {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.spacer {
  flex: 1;
}
</style>
