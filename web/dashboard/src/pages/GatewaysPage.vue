<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import Tag from 'primevue/tag'
import SettingsCard from '../components/settings/SettingsCard.vue'
import ZarinpalCard from '../components/gateways/ZarinpalCard.vue'
import KeyedGatewayCard from '../components/gateways/KeyedGatewayCard.vue'
import UsdtAutoCard from '../components/gateways/UsdtAutoCard.vue'
import { api } from '../api'
import { errorText } from '../errors'
import { isKeyedProvider, methodCards, stateSeverity, usdtAuto } from '../gateways'
import type { GatewaysReply } from '../types'

// How customers pay (owner only): Zarinpal, whose merchant id and proxy stay
// encrypted in the payments service, IDPay and NextPay (an API key and a proxy,
// kept the same way), automatic USDT (TRC20), whose address,
// TronGrid key and proxy are kept there too, then a card per manual method and
// Telegram Stars, whose switches and details are bot settings (each card
// saves on its own through the settings endpoint).
const { t, te } = useI18n()

const reply = ref<GatewaysReply | null>(null)
const loading = ref(true)
const loadError = ref('')
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    reply.value = await api<GatewaysReply>('GET', '/gateways')
  } catch (e) {
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(load)

const zarinpal = computed(() => reply.value?.providers.find((p) => p.provider === 'zarinpal') ?? null)
const keyed = computed(() => (reply.value?.providers ?? []).filter((p) => isKeyedProvider(p.provider)))
const usdt = computed(() => reply.value?.providers.find((p) => p.provider === usdtAuto) ?? null)
const keyMissing = computed(() => (reply.value?.providers ?? []).some((p) => p.key_missing))
const cards = computed(() => methodCards(reply.value?.settings ?? []))
const text = (key: string) => (te(key) ? t(key) : '')
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.gateways.title') }}</h1>
  </div>
  <p class="app-muted intro">{{ t('gateway.intro') }}</p>

  <Message v-if="keyMissing" severity="warn" icon="pi pi-key" :closable="false" class="banner key-missing-banner">
    {{ t('gateway.key_missing_banner') }}
  </Message>
  <Message v-if="loadError" severity="error" :closable="false" class="banner">
    <div class="load-error">
      <span>{{ loadError }}</span>
      <Button :label="t('app.retry')" icon="pi pi-refresh" size="small" severity="secondary" outlined @click="load" />
    </div>
  </Message>

  <div v-if="loading && !reply" class="cards" aria-busy="true">
    <Skeleton height="18rem" border-radius="var(--app-radius)" class="wide" />
    <Skeleton v-for="n in 4" :key="n" height="12rem" border-radius="var(--app-radius)" />
  </div>
  <div v-else-if="reply" class="cards">
    <ZarinpalCard v-if="zarinpal" :config="zarinpal" class="wide" @saved="load" />
    <KeyedGatewayCard v-for="k in keyed" :key="k.provider" :config="k" class="wide" @saved="load" />
    <UsdtAutoCard v-if="usdt" :config="usdt" class="wide" @saved="load" />
    <SettingsCard
      v-for="c in cards"
      :key="c.key"
      :group="c.key === 'other' ? 'payments' : c.key"
      :items="c.items"
      :title="c.key === 'other' ? undefined : t('gateway.method.' + c.key + '.title')"
      :desc="c.key === 'other' ? undefined : text('gateway.method.' + c.key + '.desc')"
      :class="['method-card', 'method-' + c.key]"
      @saved="load"
    >
      <template v-if="c.state" #badge>
        <Tag :severity="stateSeverity[c.state]" :value="t('gateway.method_state.' + c.state)" class="method-state" />
      </template>
    </SettingsCard>
  </div>
</template>

<style scoped>
.intro {
  margin-block: calc(var(--app-gap) * -0.5) var(--app-gap);
}
.banner {
  margin-block-end: var(--app-gap);
}
.load-error {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
}
.cards {
  display: grid;
  gap: var(--app-gap);
  /* two columns where they fit, one on a phone */
  grid-template-columns: repeat(auto-fit, minmax(min(100%, max(24rem, (100% - var(--app-gap)) / 2)), 1fr));
  align-items: stretch;
}
.cards > .wide {
  grid-column: 1 / -1;
}
</style>
