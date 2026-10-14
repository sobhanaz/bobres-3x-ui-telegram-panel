<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import Tag from 'primevue/tag'
import LicenseActivate from './LicenseActivate.vue'
import LicenseImport from './LicenseImport.vue'
import { api } from '../../api'
import { errorText } from '../../errors'
import { date, dateTime, isolate, num, type Lang } from '../../format'
import {
  basisOf,
  bindings,
  bindingSeverity,
  entitlementRows,
  graceEndsAt,
  licenseEnds,
  licenseSeverity,
  limitSeverity,
  reasonKey,
  renews,
  sourceKey,
  stateOf,
  TRIAL_DAYS,
  timeLeft,
  trialSeverity,
  warnings,
  type EntitlementRow,
} from '../../license'
import { useAuth } from '../../stores/auth'
import type { LicenseInfo } from '../../types'

// The license: what applies now (a license, its grace period, the 30-day
// trial or the free tier) with whom to ask for a license, the installed
// license's state, what it binds to, what applies against what this install
// uses, and for the owner a way to add or replace it: activated online with
// the seller's key, or a token pasted for an install that cannot reach the
// license server. Admins see it read only.
const { t, te, locale } = useI18n()
const auth = useAuth()
const toast = useToast()
const lang = computed(() => locale.value as Lang)

const info = ref<LicenseInfo | null>(null)
const loading = ref(true)
const loadError = ref('')
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    info.value = await api<LicenseInfo>('GET', '/license')
  } catch (e) {
    loadError.value = t('license.load_error') + ' ' + errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(load)

// A saved license changes the footer and the menu's chip too.
async function saved() {
  await Promise.all([load(), auth.refresh()])
}

const state = computed(() => stateOf(info.value?.state))
const lic = computed(() => info.value?.license ?? null)
const left = computed(() => (lic.value ? timeLeft(lic.value) : null))
const bound = computed(() => (info.value ? bindings(info.value) : null))
const domainBinding = computed(() => bound.value?.domain ?? null)
const installBinding = computed(() => bound.value?.install_id ?? null)
const savedBy = computed(() => {
  const u = info.value?.updated_by
  return u ? isolate(u.username ? '@' + u.username : u.id) : ''
})
const source = computed(() => sourceKey(info.value?.source))
const refreshedAt = computed(() => info.value?.refreshed_at ?? 0)
const renewing = computed(() => (info.value ? renews(info.value) : false))
const endsOn = computed(() => (lic.value ? licenseEnds(lic.value) : null))
const rows = computed(() => (info.value ? entitlementRows(info.value) : []))
const warned = computed(() => warnings(rows.value) > 0)
const canWrite = computed(() => auth.can('license.write'))

// What applies now: core's answer (an older core says nothing, and the page
// reads the license state instead).
const effective = computed(() => info.value?.effective ?? null)
const basis = computed(() => basisOf(effective.value?.basis, info.value?.state))
const trialDays = computed(() => Math.max(0, effective.value?.trial_days_left ?? 0))
// The trial's tag warns from its last 7 days (core raises its staff alert then too).
const planSeverity = computed(
  () => ({ free: 'warn', trial: trialSeverity(trialDays.value), grace: 'warn', license: 'success' })[basis.value] as 'warn' | 'info' | 'success',
)
const planName = computed(() => {
  switch (basis.value) {
    case 'free':
      return t('license.plan.free')
    case 'trial':
      return t('license.plan.trial', { n: num(trialDays.value, lang.value) }, trialDays.value)
    default:
      return tierName(effective.value?.tier || lic.value?.tier || '')
  }
})
const trialEnds = computed(() => effective.value?.trial_ends_at ?? 0)
// What the plan hint fills in: the trial's end and its length (core says it; an older core: 30 days).
const planParams = computed(() => ({
  date: trialEnds.value ? date(trialEnds.value, lang.value) : '',
  days: num(effective.value?.trial_days ?? TRIAL_DAYS, lang.value),
}))
// "No license" says what applies from the plan card above (the same words as its hint, so it
// never says the trial runs when it is over); an older core has no plan card, so the hint says it here.
const stateHint = computed(() => {
  if (state.value !== 'missing') return t('license.state_hint.' + state.value)
  const base = t('license.state_hint.missing')
  return effective.value ? base : base + ' ' + t('license.plan.hint.' + basis.value, planParams.value)
})

const tierName = (tier: string) => (te('license.tier.' + tier) ? t('license.tier.' + tier) : tier)
const providerName = (p: string) => (te('payment.provider.' + p) ? t('payment.provider.' + p) : p)
const sep = computed(() => (lang.value === 'fa' ? '، ' : ', '))

function limitText(r: Extract<EntitlementRow, { kind: 'limit' }>): string {
  if (r.state === 'unlimited') return r.used === null ? t('license.unlimited') : t('license.unlimited_used', { used: num(r.used, lang.value) })
  if (r.state === 'none') return t('license.none_allowed')
  if (r.used === null) return t('license.use_unknown', { limit: num(r.limit, lang.value) })
  return t('license.usage', { used: num(r.used, lang.value), limit: num(r.limit, lang.value) })
}
const limitWarn = (r: Extract<EntitlementRow, { kind: 'limit' }>) => r.state === 'near' || r.state === 'full' || r.state === 'over'

async function copyInstall() {
  if (!info.value?.install_id) return
  try {
    await navigator.clipboard.writeText(info.value.install_id)
    toast.add({ severity: 'info', summary: t('common.copied'), life: 2000 })
  } catch {
    /* clipboard blocked: the id is selectable */
  }
}
</script>

<template>
  <div class="license">
    <Message v-if="loadError" severity="error" :closable="false" class="load-error">
      <div class="load-error-row">
        <span>{{ loadError }}</span>
        <Button :label="t('app.retry')" icon="pi pi-refresh" size="small" severity="secondary" outlined @click="load" />
      </div>
    </Message>

    <div v-if="loading && !info" class="cards" aria-busy="true">
      <Skeleton v-for="n in 2" :key="n" height="12rem" border-radius="var(--app-radius)" />
    </div>
    <template v-else-if="info">
      <Card v-if="effective" class="plan-card">
        <template #title>
          <div class="title-row">
            <span>{{ t('license.plan.title') }}</span>
            <Tag :severity="planSeverity" :value="planName" class="plan-name" />
          </div>
        </template>
        <template #content>
          <p class="plan-hint">{{ t('license.plan.hint.' + basis, planParams) }}</p>
          <p v-if="basis === 'trial' && trialEnds" class="plan-trial app-muted">
            {{ t('license.plan.trial_ends', { date: date(trialEnds, lang), n: num(trialDays, lang) }, trialDays) }}
          </p>
          <p v-else-if="basis === 'free' && trialEnds" class="plan-trial app-muted">{{ t('license.plan.trial_ended', { date: date(trialEnds, lang) }) }}</p>
          <p v-if="effective.contact" class="plan-contact">
            {{ t('license.plan.contact', { contact: isolate(effective.contact) }) }}
          </p>
          <!-- the owner did not set a seller contact (BOBRES_SELLER_CONTACT): no made-up one -->
          <p v-else class="plan-contact plan-contact-none">{{ t('license.plan.contact_none') }}</p>
        </template>
      </Card>

      <div class="cards">
        <Card class="state-card">
          <template #title>
            <div class="title-row">
              <span>{{ t('license.details') }}</span>
              <Tag :severity="licenseSeverity[state]" :value="t('license.state.' + state)" class="license-state" />
            </div>
          </template>
          <template #content>
            <p class="state-hint">{{ stateHint }}</p>
            <Message v-if="state === 'invalid'" severity="error" :closable="false" class="reason">{{ t(reasonKey(info.reason)) }}</Message>
            <Message v-if="info.keys === 0" severity="info" :closable="false" class="no-keys">{{ t('license.no_keys') }}</Message>
            <dl v-if="lic" class="app-facts facts">
              <dt>{{ t('license.tier_label') }}</dt>
              <dd class="tier">{{ tierName(lic.tier) }}</dd>
              <dt>{{ t('license.customer') }}</dt>
              <dd><bdi>{{ lic.customer }}</bdi></dd>
              <dt>{{ t('license.license_id') }}</dt>
              <dd class="app-ltr app-mono license-id">{{ lic.license_id }}</dd>
              <dt>{{ t('license.kid') }}</dt>
              <dd class="app-ltr app-mono kid">{{ lic.kid }}</dd>
              <dt>{{ t('license.issued') }}</dt>
              <dd>{{ date(lic.issued_at, lang) }}</dd>
              <!-- a short token the license server renews: the license's own end first -->
              <template v-if="endsOn">
                <dt>{{ t('license.license_ends') }}</dt>
                <dd class="license-ends">{{ date(endsOn, lang) }}</dd>
              </template>
              <dt>{{ t(renewing ? 'license.token_expires' : 'license.expires') }}</dt>
              <dd class="expires">
                {{ date(lic.expires_at, lang) }}
                <span v-if="left" :class="['days-left', 'app-muted', left.kind]">· {{ t('license.left.' + left.kind, { n: num(left.days, lang) }, left.days) }}</span>
              </dd>
              <template v-if="lic.grace_days > 0">
                <dt>{{ t('license.grace_ends') }}</dt>
                <dd class="grace-ends">{{ date(graceEndsAt(lic), lang) }}</dd>
              </template>
            </dl>
            <p v-if="renewing" class="app-muted renews-hint">{{ t('license.renews_hint') }}</p>
            <p v-if="info.updated_at" class="app-muted updated">
              {{ savedBy ? t('license.updated_by', { time: dateTime(info.updated_at, lang), name: savedBy }) : t('license.updated_at', { time: dateTime(info.updated_at, lang) }) }}
              <span v-if="source" class="source">· {{ t(source) }}</span>
            </p>
            <p v-if="refreshedAt > 0" class="app-muted refreshed">{{ t('license.refreshed_at', { time: dateTime(refreshedAt, lang) }) }}</p>
          </template>
        </Card>

        <Card class="install-card">
          <template #title>{{ t('license.this_install') }}</template>
          <template #content>
            <dl class="app-facts facts">
              <dt>{{ t('license.install_id') }}</dt>
              <dd class="install-row">
                <span class="app-ltr app-mono install-id">{{ info.install_id || '—' }}</span>
                <Button
                  v-if="info.install_id"
                  icon="pi pi-copy"
                  text
                  rounded
                  size="small"
                  :aria-label="t('license.copy_install_id')"
                  v-tooltip.top="t('common.copy')"
                  class="copy-install"
                  @click="copyInstall"
                />
              </dd>
              <dt>{{ t('license.domain') }}</dt>
              <dd class="app-ltr this-domain">{{ info.domain || t('license.domain_none') }}</dd>
            </dl>
            <p class="app-muted install-hint">{{ t('license.install_id_hint') }}</p>
            <template v-if="lic">
              <h3 class="sub">{{ t('license.binding') }}</h3>
              <dl class="app-facts facts">
                <dt>{{ t('license.binding_domain') }}</dt>
                <dd class="binding-domain">
                  <Tag v-if="domainBinding" :severity="bindingSeverity[domainBinding]" :value="t('license.binding_state.' + domainBinding)" />
                  <span v-if="lic.domain" class="app-muted bound">{{ t('license.bound_to', { value: isolate(lic.domain) }) }}</span>
                </dd>
                <dt>{{ t('license.binding_install') }}</dt>
                <dd class="binding-install">
                  <Tag v-if="installBinding" :severity="bindingSeverity[installBinding]" :value="t('license.binding_state.' + installBinding)" />
                  <span v-if="lic.install_id" class="app-muted bound">{{ t('license.bound_to', { value: isolate(lic.install_id) }) }}</span>
                </dd>
              </dl>
            </template>
          </template>
        </Card>
      </div>

      <Card v-if="rows.length" class="entitlements">
        <template #title>{{ t('license.entitlements') }}</template>
        <template #content>
          <p class="app-muted ent-source">{{ t('license.entitlements_from.' + basis) }}</p>
          <Message v-if="warned" severity="warn" :closable="false" class="limits-warn">{{ t('license.warnings') }}</Message>
          <div v-for="r in rows" :key="r.key" :class="['ent', 'ent-' + r.key]">
            <span class="ent-name">{{ t('license.ent.' + r.key) }}</span>
            <div class="ent-value">
              <template v-if="r.kind === 'limit'">
                <span class="use">{{ limitText(r) }}</span>
                <Tag v-if="limitWarn(r)" :severity="limitSeverity[r.state]" :value="t('license.limit_state.' + r.state)" class="limit-state" />
              </template>
              <Tag v-else-if="r.kind === 'feature'" :severity="r.on ? 'success' : 'secondary'" :value="t('license.feature.' + (r.on ? 'on' : 'off'))" class="feature" />
              <template v-else-if="r.kind === 'gateways'">
                <span class="allowed">{{ r.allowed.length ? r.allowed.map(providerName).join(sep) : t('license.gateways_none') }}</span>
                <small class="app-muted">{{ t('license.wallet_always') }}</small>
                <Message v-if="r.outside.length" severity="warn" :closable="false" class="outside">
                  {{ t('license.gateways_outside', { list: r.outside.map(providerName).join(sep) }) }}
                </Message>
              </template>
              <span v-else class="app-ltr updates">{{ r.value || t('license.updates_any') }}</span>
            </div>
          </div>
        </template>
      </Card>

      <template v-if="canWrite">
        <LicenseActivate @saved="saved" />
        <LicenseImport :replacing="state !== 'missing'" @saved="saved" />
      </template>
      <p v-else class="app-muted owner-only">{{ t('license.owner_only') }}</p>
    </template>
  </div>
</template>

<style scoped>
.license {
  display: flex;
  flex-direction: column;
  gap: var(--app-gap);
}
.load-error-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
}
.cards {
  display: grid;
  gap: var(--app-gap);
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 24rem), 1fr));
  align-items: stretch;
}
.title-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
}
.state-hint {
  margin-block-start: 0;
}
.reason,
.no-keys {
  margin-block-end: var(--app-gap);
}
.facts dd {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.4rem;
}
.install-id {
  overflow-wrap: anywhere;
}
.install-hint,
.renews-hint,
.updated {
  margin-block: 0.75rem 0;
  font-size: 0.9rem;
}
.refreshed {
  margin-block: 0.25rem 0;
  font-size: 0.9rem;
}
.sub {
  font-size: 1rem;
  margin-block: 1rem 0.5rem;
}
.ent {
  display: grid;
  grid-template-columns: minmax(10rem, 18rem) 1fr;
  gap: 0.4rem 1rem;
  padding-block: 0.6rem;
  align-items: baseline;
}
.ent + .ent {
  border-block-start: 1px solid var(--p-content-border-color);
}
.ent-name {
  color: var(--p-text-muted-color);
}
.ent-value {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
.outside {
  flex-basis: 100%;
}
.limits-warn {
  margin-block-end: 0.5rem;
}
.plan-hint {
  margin-block-start: 0;
}
.plan-trial,
.plan-contact {
  margin-block: 0.5rem 0;
}
.ent-source {
  margin-block: 0 0.5rem;
}
.owner-only {
  margin: 0;
}
@media (max-width: 640px) {
  .ent {
    grid-template-columns: 1fr;
  }
}
</style>
