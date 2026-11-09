<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { Service } from '../api'
import { errorKey } from '../errors'
import { tr } from '../i18n'
import { fmtBytes, serviceEnd, statusKey } from '../fmt'
import { fail, loadServices, state } from '../store'
import { copyText, openLink } from '../telegram'
import RenameForm from '../sections/names/RenameForm.vue'

const emit = defineEmits<{ renew: [s: Service]; topup: [s: Service] }>()
const error = ref('')
const copied = ref('')
// the service whose config name is being changed, and the one just renamed (a line says so)
const renaming = ref('')
const renamed = ref('')

async function load() {
  error.value = ''
  try {
    await loadServices()
  } catch (e) {
    if (!fail(e)) error.value = tr(errorKey(e))
  }
}
onMounted(load)

async function copy(s: Service) {
  try {
    await copyText(s.link)
    copied.value = s.id
    setTimeout(() => (copied.value = ''), 1500)
  } catch {
    // no clipboard
  }
}

/** done puts the renamed service in the list as core answered it (or just its new name, from an older answer). */
function done(s: Service, name: string, fresh: Service | undefined) {
  renaming.value = ''
  renamed.value = s.id
  const list = state.services
  if (!list) return
  const i = list.findIndex((x) => x.id === s.id)
  if (i >= 0) list[i] = fresh ?? { ...list[i], config_name: name }
}

function usage(s: Service): string {
  return s.traffic_total
    ? tr('services.traffic', { used: fmtBytes(s.traffic_used), total: fmtBytes(s.traffic_total) })
    : tr('services.traffic_free', { used: fmtBytes(s.traffic_used) })
}

const percent = (s: Service) => (s.traffic_total ? Math.min(100, Math.round((s.traffic_used / s.traffic_total) * 100)) : 0)
</script>

<template>
  <section>
    <h1>{{ tr('services.title') }}</h1>
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>
    <p v-else-if="state.services === null" class="muted">{{ tr('common.loading') }}</p>
    <p v-else-if="!state.services.length" class="muted">{{ tr('services.empty') }}</p>
    <ul v-else class="list">
      <li v-for="s in state.services" :key="s.id" class="card">
        <div class="row between">
          <strong>{{ s.label }}</strong>
          <span class="badge" :class="s.status">{{ tr(statusKey(s.status)) }}</span>
        </div>
        <p v-if="s.config_name" class="muted small" dir="auto">{{ tr('names.current', { name: s.config_name }) }}</p>
        <p class="muted">{{ serviceEnd(s) }}</p>
        <p class="muted">{{ usage(s) }}</p>
        <div v-if="s.traffic_total" class="bar" role="progressbar" :aria-valuenow="percent(s)" aria-valuemin="0" aria-valuemax="100">
          <span :style="{ width: percent(s) + '%' }"></span>
        </div>
        <div class="row wrap">
          <button v-if="s.link" class="btn secondary" type="button" @click="copy(s)">
            {{ copied === s.id ? tr('common.copied') : tr('services.link') }}
          </button>
          <button v-if="s.page_url" class="btn secondary" type="button" @click="openLink(s.page_url)">{{ tr('services.page') }}</button>
          <button v-if="s.can_renew" class="btn" type="button" @click="emit('renew', s)">{{ tr('services.renew') }}</button>
          <button v-if="s.can_topup" class="btn" type="button" @click="emit('topup', s)">{{ tr('services.topup') }}</button>
          <button v-if="s.renamable" class="btn secondary" type="button" :aria-expanded="renaming === s.id" @click="renaming = renaming === s.id ? '' : s.id">
            {{ tr('names.rename') }}
          </button>
        </div>
        <RenameForm v-if="renaming === s.id" :service-id="s.id" :current="s.config_name" @done="(name, fresh) => done(s, name, fresh)" @cancel="renaming = ''" />
        <p v-else-if="renamed === s.id && s.config_name" class="ok" role="status">{{ tr('names.renamed', { name: s.config_name }) }}</p>
      </li>
    </ul>
  </section>
</template>
