<script setup lang="ts">
// One of the reseller's customer services: its name, status, time left and
// usage, and (opened) what can be done to it: the link and page to hand to
// the customer, renew and add traffic through the shop's own checkout, a new
// name, a private note, pause or resume, and delete after a confirmation.
// Core decides what is allowed; the buttons follow the can_* answers.
import { computed, ref } from 'vue'
import { fmtBytes, fmtDate, fmtNum, statusKey } from '../../fmt'
import { daysLeft, isolate } from '../../format'
import { haptic, copyText, openLink } from '../../telegram'
import { tr } from '../../i18n'
import { fail } from '../../store'
import { errText, isGone, resellerApi, type ResellerService } from './api'
import { cleanLabel, cleanNote, MAX_LABEL, MAX_NOTE } from './text'

const props = defineProps<{ service: ResellerService; open: boolean }>()
const emit = defineEmits<{ toggle: []; renew: []; topup: []; changed: [done: string] }>()

type Mode = '' | 'rename' | 'note' | 'delete'
const mode = ref<Mode>('')
const draft = ref('')
const busy = ref(false)
const error = ref('')
const copied = ref(false)

const shortId = computed(() => props.service.id.replace(/-/g, '').slice(-6))
const title = computed(() => props.service.label || tr('reseller.services.unnamed', { id: isolate(shortId.value) }))
const status = computed(() => (props.service.status === 'disabled' ? tr('reseller.status.disabled') : tr(statusKey(props.service.status))))
const pending = computed(() => props.service.status === 'pending')

const expiry = computed(() => {
  const at = props.service.expires_at
  if (!at) return ''
  const n = daysLeft(at, Date.now())
  return n < 0 ? tr('services.expired') : tr('services.days_left', { n: fmtNum(n) }) + ' · ' + tr('services.expires', { date: fmtDate(at) })
})
const usage = computed(() =>
  props.service.traffic_total
    ? tr('services.traffic', { used: fmtBytes(props.service.traffic_used), total: fmtBytes(props.service.traffic_total) })
    : tr('services.traffic_free', { used: fmtBytes(props.service.traffic_used) }),
)
const percent = computed(() =>
  props.service.traffic_total ? Math.min(100, Math.round((props.service.traffic_used / props.service.traffic_total) * 100)) : 0,
)

function edit(m: Mode) {
  error.value = ''
  mode.value = m
  draft.value = m === 'rename' ? props.service.label : m === 'note' ? props.service.note : ''
}

/** run does one call for this service and tells the list what happened. */
async function run(call: () => Promise<unknown>, done: string) {
  busy.value = true
  error.value = ''
  try {
    await call()
    haptic('success')
    mode.value = ''
    emit('changed', done)
  } catch (e) {
    haptic('error')
    if (!fail(e)) error.value = errText(e)
    // a service that is gone (or not theirs) leaves the list on the next load
    if (isGone(e)) emit('changed', '')
  } finally {
    busy.value = false
  }
}

const pause = () => run(() => resellerApi.pause(props.service.id), 'reseller.done.paused')
const resume = () => run(() => resellerApi.resume(props.service.id), 'reseller.done.resumed')
const remove = () => run(() => resellerApi.remove(props.service.id), 'reseller.done.deleted')

function saveName() {
  const label = cleanLabel(draft.value)
  if (label === null) error.value = tr('reseller.err.bad_text')
  else void run(() => resellerApi.rename(props.service.id, label), 'reseller.done.saved')
}

function saveNote() {
  const note = cleanNote(draft.value)
  if (note === null) error.value = tr('reseller.err.bad_text')
  else void run(() => resellerApi.note(props.service.id, note), 'reseller.done.saved')
}

async function copy() {
  try {
    await copyText(props.service.link)
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  } catch {
    // no clipboard
  }
}
</script>

<template>
  <li class="card rs-service">
    <button class="rs-head" type="button" :aria-expanded="open" @click="emit('toggle')">
      <span class="row between">
        <strong>{{ title }}</strong>
        <span class="badge" :class="service.status">{{ status }}</span>
      </span>
      <span v-if="expiry" class="muted small rs-line">{{ expiry }}</span>
      <span class="muted small rs-line">{{ usage }}</span>
      <span v-if="service.traffic_total" class="bar" role="progressbar" :aria-valuenow="percent" aria-valuemin="0" aria-valuemax="100">
        <span :style="{ width: percent + '%' }"></span>
      </span>
      <span v-if="service.note" class="rs-note">📝 {{ service.note }}</span>
    </button>

    <div v-if="open" class="rs-actions">
      <p v-if="pending" class="muted">{{ tr('reseller.services.pending') }}</p>
      <div class="row wrap">
        <button v-if="service.link" class="btn secondary" type="button" @click="copy">{{ copied ? tr('common.copied') : tr('services.link') }}</button>
        <button v-if="service.page_url" class="btn secondary" type="button" @click="openLink(service.page_url)">{{ tr('services.page') }}</button>
        <button v-if="service.can_renew" class="btn" type="button" @click="emit('renew')">{{ tr('services.renew') }}</button>
        <button v-if="service.can_topup" class="btn" type="button" @click="emit('topup')">{{ tr('services.topup') }}</button>
      </div>
      <div class="row wrap">
        <button v-if="service.can_pause" class="btn secondary" type="button" :disabled="busy" @click="pause">
          {{ tr('reseller.action.pause') }}
        </button>
        <button v-if="service.can_resume" class="btn" type="button" :disabled="busy" @click="resume">
          {{ tr('reseller.action.resume') }}
        </button>
        <button class="btn secondary" type="button" :aria-expanded="mode === 'rename'" @click="edit(mode === 'rename' ? '' : 'rename')">{{ tr('reseller.action.rename') }}</button>
        <button class="btn secondary" type="button" :aria-expanded="mode === 'note'" @click="edit(mode === 'note' ? '' : 'note')">{{ tr('reseller.action.note') }}</button>
        <button v-if="service.can_delete" class="btn secondary rs-danger" type="button" :aria-expanded="mode === 'delete'" @click="edit(mode === 'delete' ? '' : 'delete')">
          {{ tr('reseller.action.delete') }}
        </button>
      </div>

      <form v-if="mode === 'rename'" class="rs-grid" @submit.prevent="saveName">
        <label class="field">
          <span>{{ tr('reseller.rename.label') }}</span>
          <input v-model="draft" type="text" :maxlength="MAX_LABEL" autocomplete="off" enterkeyhint="done" />
        </label>
        <div class="row wrap">
          <button class="btn" type="submit" :disabled="busy">{{ tr('reseller.action.save') }}</button>
          <button class="btn secondary" type="button" @click="edit('')">{{ tr('reseller.action.cancel') }}</button>
        </div>
      </form>

      <form v-else-if="mode === 'note'" class="rs-grid" @submit.prevent="saveNote">
        <label class="field">
          <span>{{ tr('reseller.note.label') }}</span>
          <input v-model="draft" type="text" :maxlength="MAX_NOTE" autocomplete="off" enterkeyhint="done" />
          <span class="muted small">{{ tr('reseller.note.hint', { max: fmtNum(MAX_NOTE) }) }}</span>
        </label>
        <div class="row wrap">
          <button class="btn" type="submit" :disabled="busy">{{ tr('reseller.action.save') }}</button>
          <button class="btn secondary" type="button" @click="edit('')">{{ tr('reseller.action.cancel') }}</button>
        </div>
      </form>

      <div v-else-if="mode === 'delete'" class="rs-confirm" role="alertdialog" :aria-label="tr('reseller.delete.title')">
        <strong>{{ tr('reseller.delete.title') }}</strong>
        <p>{{ tr('reseller.delete.warning') }}</p>
        <div class="row wrap">
          <button class="btn rs-danger-fill" type="button" :disabled="busy" @click="remove">
            {{ tr('reseller.delete.yes') }}
          </button>
          <button class="btn secondary" type="button" @click="edit('')">{{ tr('reseller.delete.no') }}</button>
        </div>
      </div>

      <p v-if="error" class="error" role="alert">{{ error }}</p>
    </div>
  </li>
</template>
