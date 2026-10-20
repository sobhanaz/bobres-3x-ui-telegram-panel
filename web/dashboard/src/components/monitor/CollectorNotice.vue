<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import Message from 'primevue/message'
import { collectorState, ENABLE_COMMAND, STATUS_COMMAND, type Overview } from '../../monitor'
import { copyText } from '../../updates'
import { useFmt } from './feed'

// The state of a server without host figures: the host collector (a root timer on the server, on by default
// since it was introduced) is not installed, has gone quiet, or writes a file core cannot use. It says which,
// since when, and the command that turns it on; never zeros in place of figures.
const props = defineProps<{ overview: Pick<Overview, 'host' | 'collector'> }>()
const { t } = useI18n()
const toast = useToast()
const { dur } = useFmt()

const state = computed(() => collectorState(props.overview))
const title = computed(() => {
  const c = props.overview.collector
  switch (state.value) {
    case 'quiet':
      return t('monitor.collector.quiet', { age: dur(c.age_s) })
    case 'unusable':
      return t('monitor.collector.unusable', { error: c.error })
    case 'no_host':
      return t('monitor.collector.no_host')
    default:
      return t('monitor.collector.not_installed')
  }
})

const copied = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
async function copy(text: string) {
  if (await copyText(text)) {
    copied.value = true
    clearTimeout(timer)
    timer = setTimeout(() => (copied.value = false), 2000)
    toast.add({ severity: 'info', summary: t('common.copied'), life: 2000 })
  } else {
    toast.add({ severity: 'warn', summary: t('system.updates.copy_failed'), life: 6000 })
  }
}
</script>

<template>
  <Message severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="collector-notice">
    <div class="notice">
      <strong class="notice-title">{{ title }}</strong>
      <p class="notice-body">{{ t('monitor.collector.body') }}</p>
      <p class="notice-how">{{ t('monitor.collector.enable') }}</p>
      <div class="command-row">
        <code class="app-ltr app-mono command enable-command">{{ ENABLE_COMMAND }}</code>
        <Button
          icon="pi pi-copy"
          text
          rounded
          size="small"
          :aria-label="t('monitor.collector.copy')"
          class="copy-enable"
          @click="copy(ENABLE_COMMAND)"
        />
      </div>
      <template v-if="state === 'quiet' || state === 'unusable'">
        <p class="notice-how">{{ t('monitor.collector.status') }}</p>
        <div class="command-row">
          <code class="app-ltr app-mono command status-command">{{ STATUS_COMMAND }}</code>
        </div>
      </template>
      <p v-if="overview.host.reason" class="app-muted app-ltr reason" dir="ltr">{{ overview.host.reason }}</p>
    </div>
  </Message>
</template>

<style scoped>
.notice {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  min-inline-size: 0;
}
.notice p {
  margin: 0;
}
.command-row {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  min-inline-size: 0;
}
.command {
  padding: 0.35rem 0.6rem;
  border-radius: 0.4rem;
  background: var(--p-content-background);
  border: 1px solid var(--p-content-border-color);
  overflow-wrap: anywhere;
  min-inline-size: 0;
}
.reason {
  font-size: 0.8rem;
  overflow-wrap: anywhere;
}
</style>
