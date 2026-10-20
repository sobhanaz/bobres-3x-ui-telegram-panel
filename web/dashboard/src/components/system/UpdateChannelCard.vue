<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Message from 'primevue/message'
import SelectButton from 'primevue/selectbutton'
import { isolate } from '../../format'

// The channel updates follow: stable (releases that are not pre-releases) or
// beta (the newest of all releases, pre-releases included). It is a setting the
// owner makes here; unset, the server's BOBRES_UPDATE_CHANNEL (in its .env)
// decides. Changing it makes the bot look again at once.
const props = defineProps<{
  /** what the owner chose here ('' none) */
  setting: string
  /** the channel the bot follows now ('' not known) */
  effective: string
  /** setting | server: where the channel comes from */
  source: string
  /** the staff member may change it (the owner) */
  canEdit: boolean
  busy: boolean
  error: string
}>()
const emit = defineEmits<{ save: [channel: 'stable' | 'beta' | '']; reset: [] }>()
const { t } = useI18n()

type Choice = 'stable' | 'beta'
const current = computed<Choice>(() => (props.effective === 'beta' || (props.effective === '' && props.setting === 'beta') ? 'beta' : 'stable'))
const selected = ref<Choice>(current.value)
// follows what core says, after a save or a look
watch(current, (v) => (selected.value = v))
const options = computed(() => [
  { value: 'stable' as Choice, label: t('system.updates.channel_card.stable') },
  { value: 'beta' as Choice, label: t('system.updates.channel_card.beta') },
])
const dirty = computed(() => selected.value !== current.value)
</script>

<template>
  <Card class="channel-card">
    <template #title>{{ t('system.updates.channel_card.title') }}</template>
    <template #content>
      <p class="app-muted intro">{{ t('system.updates.channel_card.intro') }}</p>
      <SelectButton
        v-model="selected"
        :options="options"
        option-label="label"
        option-value="value"
        :allow-empty="false"
        :disabled="!canEdit || busy"
        :aria-label="t('system.updates.channel_card.title')"
        class="choice"
      />
      <p class="app-muted hint">{{ selected === 'beta' ? t('system.updates.channel_card.beta_hint') : t('system.updates.channel_card.stable_hint') }}</p>
      <Message v-if="selected === 'beta'" severity="warn" icon="pi pi-flag" :closable="false" class="beta-warning">
        {{ t('system.updates.channel_card.beta_warning') }}
      </Message>
      <p class="app-muted source">
        {{ source === 'setting' ? t('system.updates.channel_card.source_setting') : t('system.updates.channel_card.source_server', { name: isolate('BOBRES_UPDATE_CHANNEL') }) }}
      </p>
      <Message v-if="error" severity="error" :closable="false" class="channel-error">{{ error }}</Message>
      <p v-if="!canEdit" class="app-muted owner-only">{{ t('system.updates.channel_card.owner_only') }}</p>
      <div v-else class="actions">
        <Button :label="t('system.updates.channel_card.save')" icon="pi pi-check" :loading="busy" :disabled="!dirty" class="save" @click="emit('save', selected)" />
        <Button
          v-if="source === 'setting'"
          :label="t('system.updates.channel_card.reset')"
          text
          size="small"
          :disabled="busy"
          class="reset"
          @click="emit('reset')"
        />
      </div>
    </template>
  </Card>
</template>

<style scoped>
.intro {
  margin-block: 0 0.75rem;
}
.hint,
.source {
  margin-block: 0.6rem 0;
  font-size: 0.9rem;
}
.beta-warning {
  margin-block-start: 0.75rem;
}
.channel-error {
  margin-block-start: 0.75rem;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
  margin-block-start: 0.9rem;
}
.owner-only {
  margin-block: 0.75rem 0;
}
</style>
