<script setup lang="ts">
// Invite friends: the customer's own link (the bot's start link with their
// code), how many joined through it and what it earned.
import { onMounted, ref } from 'vue'
import { errorKey } from '../../errors'
import { fmtMoney, fmtNum } from '../../fmt'
import { isolate } from '../../format'
import { tr } from '../../i18n'
import { fail, state } from '../../store'
import { copyText, openTelegram } from '../../telegram'
import { inviteApi, type Invite } from './api'

const info = ref<Invite | null>(null)
const error = ref('')
const copied = ref(false)

async function load() {
  error.value = ''
  try {
    info.value = await inviteApi.get()
  } catch (e) {
    if (!fail(e)) error.value = tr(errorKey(e))
  }
}
onMounted(load)

async function copy() {
  if (!info.value) return
  try {
    await copyText(info.value.link)
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  } catch {
    // no clipboard
  }
}

// Telegram's own share sheet, with the link and a line to go with it.
function share() {
  if (!info.value) return
  const text = tr('invite.share_text', { brand: state.me?.brand.name || '' })
  openTelegram(`https://t.me/share/url?url=${encodeURIComponent(info.value.link)}&text=${encodeURIComponent(text)}`)
}
</script>

<template>
  <section>
    <h1>{{ tr('invite.title') }}</h1>
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>
    <p v-else-if="info === null" class="muted">{{ tr('common.loading') }}</p>
    <template v-else>
      <p class="muted">{{ tr('invite.intro', { percent: fmtNum(info.percent) }) }}</p>
      <section class="card">
        <span class="muted small">{{ tr('invite.link') }}</span>
        <p class="mono" dir="ltr">{{ info.link }}</p>
        <div class="row wrap">
          <button class="btn secondary" type="button" @click="copy">{{ copied ? tr('common.copied') : tr('invite.copy') }}</button>
          <button class="btn" type="button" @click="share">{{ tr('invite.share') }}</button>
        </div>
      </section>
      <section class="card">
        <p class="row between"><span>{{ tr('invite.invited') }}</span><strong>{{ fmtNum(info.invited) }}</strong></p>
        <p class="row between"><span>{{ tr('invite.bought') }}</span><strong>{{ fmtNum(info.rewarded) }}</strong></p>
        <p v-if="!info.earned.length" class="muted">{{ tr('invite.earned_none') }}</p>
        <p v-for="e in info.earned" :key="e.currency" class="row between">
          <span>{{ tr('invite.earned') }}</span>
          <strong class="pos">{{ isolate(fmtMoney(e.amount, e.currency, e.scale)) }}</strong>
        </p>
      </section>
    </template>
  </section>
</template>
