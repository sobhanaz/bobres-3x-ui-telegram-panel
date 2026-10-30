<script setup lang="ts">
// Product news: one switch, the customer's own answer to "Do you want news of new products?" (the bot asks it once; here it can be changed
// any time). It is saved at once; a failure puts the switch back.
import { computed, ref } from 'vue'
import { errorKey } from '../../errors'
import { tr } from '../../i18n'
import { fail, state } from '../../store'
import { haptic } from '../../telegram'
import { newsApi } from './api'

const on = computed(() => state.me?.features.news_optin === true)
const busy = ref(false)
const error = ref('')

async function toggle() {
  if (busy.value || !state.me) return
  error.value = ''
  busy.value = true
  try {
    const res = await newsApi.set(!on.value)
    state.me.features.news_optin = res.opt_in
    haptic('success')
  } catch (e) {
    if (fail(e)) return
    error.value = tr(errorKey(e))
    haptic('error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section>
    <h1>{{ tr('news.title') }}</h1>
    <p class="muted">{{ tr('news.intro') }}</p>
    <div class="card">
      <button class="row between switch" type="button" role="switch" :aria-checked="on" :disabled="busy" @click="toggle">
        <span>{{ tr('news.switch') }}</span>
        <span class="knob" :class="{ on }" aria-hidden="true"><span /></span>
      </button>
      <p class="muted small" aria-live="polite">{{ tr(on ? 'news.on' : 'news.off') }}</p>
    </div>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
  </section>
</template>

<style scoped>
.switch {
  inline-size: 100%;
  background: none;
  border: 0;
  padding: 0.25rem 0;
  color: inherit;
  font: inherit;
  text-align: start;
  cursor: pointer;
  min-block-size: 2.75rem;
}
.knob {
  flex: none;
  inline-size: 2.75rem;
  block-size: 1.6rem;
  border-radius: 1rem;
  background: var(--bg);
  position: relative;
  transition: background 0.15s;
}
.knob > span {
  position: absolute;
  inset-block-start: 0.2rem;
  inset-inline-start: 0.2rem;
  inline-size: 1.2rem;
  block-size: 1.2rem;
  border-radius: 50%;
  background: var(--muted);
  transition: inset-inline-start 0.15s, background 0.15s;
}
.knob.on {
  background: var(--accent);
}
.knob.on > span {
  inset-inline-start: 1.35rem;
  background: var(--accent-fg);
}
</style>
