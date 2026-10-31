<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch, watchEffect } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Drawer from 'primevue/drawer'
import Menu from 'primevue/menu'
import Badge from 'primevue/badge'
import Tag from 'primevue/tag'
import { useAuth } from '../stores/auth'
import { useUi } from '../stores/ui'
import { groups, visibleSections } from '../sections'
import { currentLang } from '../i18n'
import LangMenu from '../components/LangMenu.vue'
import { applyDocumentBrand } from '../theme'
import { num, rtl, type Lang } from '../format'
import { loadCounts, pollEvery, poller, ticketCounts } from '../support'
import { chipOf, chipSeverityOf, poweredBy } from '../license'

const { t, locale } = useI18n()
const auth = useAuth()
const ui = useUi()
const route = useRoute()
const router = useRouter()

// The store's uploaded logo, else the BOBRES icon; the tab shows the store too.
const icon = import.meta.env.BASE_URL + 'favicon.svg'
const logo = computed(() => auth.me?.branding?.logo || icon)
watchEffect(() => applyDocumentBrand(t('branding.doc_title', { brand: auth.brand }), auth.me?.branding?.logo))
const drawer = ref(false)
watch(() => route.fullPath, () => (drawer.value = false))

// With the initial password still to be changed there is no menu: only My account works.
const restricted = computed(() => !!auth.me?.must_change)
const nav = computed(() => {
  if (restricted.value) return []
  const list = visibleSections(auth.me?.permissions ?? [])
  return groups
    .map((g) => ({ group: g, items: list.filter((s) => s.group === g) }))
    .filter((g) => g.items.length > 0)
})

// Tickets waiting for staff, on the Support item: refreshed every minute for
// those who answer tickets (and right away by the support pages).
const canTickets = computed(() => auth.can('tickets.write') && !restricted.value)
const countsPoll = poller(loadCounts, () => pollEvery.counts)
watch(canTickets, (v) => (v ? countsPoll.start() : countsPoll.stop()), { immediate: true })
onBeforeUnmount(() => countsPoll.stop())
const waiting = computed(() => (canTickets.value ? (ticketCounts.value?.waiting ?? 0) : 0))
const badgeOf = (key: string) => (key === 'support' && waiting.value > 0 ? num(waiting.value, locale.value as Lang) : '')

// What applies now when it needs a look, for staff who see the System page:
// the free tier, the trial (with its days left), or a license that was
// refused, is in its grace period or has ended. And "Powered by BOBRES" on
// every install except one whose license includes white label.
const licenseChip = computed(() => {
  if (!auth.can('system.read') || restricted.value) return null
  return chipOf(auth.me?.license)
})
const chipText = (c: { kind: string; days: number }) => t('license.chip.' + c.kind, { n: num(c.days, locale.value as Lang) }, c.days)
// The label is hidden on a phone, so the link is named for what it is: the license.
const chipAria = (c: { kind: string; days: number }) => t('license.chip.aria', { chip: chipText(c) })
const showPoweredBy = computed(() => poweredBy(auth.me?.license))

const themeIcon = computed(() => ({ system: 'pi pi-desktop', dark: 'pi pi-moon', light: 'pi pi-sun' })[ui.theme])

const userMenu = ref<InstanceType<typeof Menu> | null>(null)
const userItems = computed(() => [
  { label: t('nav.account'), icon: 'pi pi-user', command: () => router.push({ name: 'account' }) },
  { label: t('nav.logout'), icon: 'pi pi-sign-out', command: logout },
])

async function logout() {
  await auth.logout()
  await router.push({ name: 'login' })
}

</script>

<template>
  <div class="layout">
    <aside class="sidebar" :aria-label="t('nav.menu')">
      <RouterLink :to="{ name: 'overview' }" class="brand">
        <img :src="logo" alt="" width="28" height="28" />
        <span>{{ auth.brand }}</span>
      </RouterLink>
      <nav>
        <template v-for="g in nav" :key="g.group">
          <div class="group">{{ t('nav.group.' + g.group) }}</div>
          <RouterLink v-for="s in g.items" :key="s.key" :to="{ name: s.key }" class="item" :class="{ active: route.meta.section === s.key }" active-class="" exact-active-class="">
            <i :class="s.icon" aria-hidden="true" />
            <span>{{ t('section.' + s.key + '.title') }}</span>
            <template v-if="badgeOf(s.key)">
              <Badge :value="badgeOf(s.key)" severity="warn" class="badge" aria-hidden="true" />
              <span class="sr-only">{{ t('support.badge', { n: badgeOf(s.key) }, waiting) }}</span>
            </template>
          </RouterLink>
        </template>
      </nav>
    </aside>

    <Drawer v-model:visible="drawer" :position="rtl(currentLang()) ? 'right' : 'left'" class="mobile-nav" :header="auth.brand">
      <nav>
        <template v-for="g in nav" :key="g.group">
          <div class="group">{{ t('nav.group.' + g.group) }}</div>
          <RouterLink v-for="s in g.items" :key="s.key" :to="{ name: s.key }" class="item" :class="{ active: route.meta.section === s.key }" active-class="" exact-active-class="">
            <i :class="s.icon" aria-hidden="true" />
            <span>{{ t('section.' + s.key + '.title') }}</span>
            <template v-if="badgeOf(s.key)">
              <Badge :value="badgeOf(s.key)" severity="warn" class="badge" aria-hidden="true" />
              <span class="sr-only">{{ t('support.badge', { n: badgeOf(s.key) }, waiting) }}</span>
            </template>
          </RouterLink>
        </template>
      </nav>
    </Drawer>

    <div class="main">
      <header class="topbar">
        <Button class="menu-btn" icon="pi pi-bars" text rounded :aria-label="t('nav.menu')" @click="drawer = true" />
        <span class="spacer" />
        <RouterLink v-if="licenseChip" :to="{ name: 'system', query: { tab: 'license' } }" class="license-chip" :aria-label="chipAria(licenseChip)">
          <Tag :severity="chipSeverityOf(licenseChip)" :value="chipText(licenseChip)" icon="pi pi-key" />
        </RouterLink>
        <LangMenu />
        <Button :icon="themeIcon" text rounded :aria-label="t('theme.' + ui.theme)" v-tooltip.bottom="t('theme.' + ui.theme)" @click="ui.cycleTheme()" />
        <Button text class="user-btn" @click="userMenu?.toggle($event)">
          <i class="pi pi-user" aria-hidden="true" />
          <bdi>{{ auth.me?.user.username ? '@' + auth.me.user.username : auth.me?.user.telegram_id }}</bdi>
          <span class="role">{{ t('role.' + (auth.me?.user.role ?? 'admin')) }}</span>
        </Button>
        <Menu ref="userMenu" :model="userItems" popup />
      </header>
      <main class="content">
        <RouterView />
      </main>
      <footer v-if="showPoweredBy" class="attribution app-muted">
        <div class="powered-by"><img :src="icon" alt="" width="16" height="16" />{{ t('license.powered_by') }}</div>
        <div class="made-by">{{ t('license.made_by') }}</div>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.layout {
  display: flex;
  min-block-size: 100vh;
}
.sidebar {
  inline-size: var(--app-sidebar-width);
  flex: none;
  /* content-background is white in light mode and deep navy in dark mode */
  background: var(--p-content-background);
  border-inline-end: 1px solid var(--p-content-border-color);
  padding-block: 1rem;
  position: sticky;
  inset-block-start: 0;
  block-size: 100vh;
  overflow-y: auto;
}
.brand {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding-inline: 1.25rem;
  margin-block-end: 1rem;
  font-weight: 700;
  font-size: 1.1rem;
  color: var(--p-text-color);
  text-decoration: none;
}
.brand img {
  flex: none;
  object-fit: contain;
  border-radius: 0.375rem;
}
.brand span {
  min-inline-size: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
nav {
  display: flex;
  flex-direction: column;
}
.group {
  font-size: 0.75rem;
  font-weight: 700;
  color: var(--p-text-muted-color);
  padding-inline: 1.25rem;
  margin-block: 0.9rem 0.3rem;
}
.item {
  display: flex;
  align-items: center;
  gap: 0.7rem;
  margin-inline: 0.6rem;
  padding: 0.55rem 0.7rem;
  border-radius: 0.5rem;
  color: var(--p-text-color);
  text-decoration: none;
  transition: background-color 0.15s;
}
.item:hover {
  background: var(--p-content-hover-background);
}
.item.active {
  background: var(--p-highlight-background);
  color: var(--p-highlight-color);
  font-weight: 500;
}
.item i {
  font-size: 1rem;
  inline-size: 1.1rem;
  text-align: center;
}
.badge {
  margin-inline-start: auto;
}
.sr-only {
  position: absolute;
  inline-size: 1px;
  block-size: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
  border: 0;
}
.main {
  flex: 1;
  min-inline-size: 0;
  display: flex;
  flex-direction: column;
}
.topbar {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.5rem 1rem;
  background: var(--p-content-background);
  border-block-end: 1px solid var(--p-content-border-color);
  position: sticky;
  inset-block-start: 0;
  z-index: 2;
}
.spacer {
  flex: 1;
}
.menu-btn {
  display: none;
}
.user-btn {
  gap: 0.45rem;
}
.role {
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
}
.license-chip {
  display: inline-flex;
  margin-inline-end: 0.25rem;
  text-decoration: none;
}
.attribution {
  margin-block-start: auto;
  padding: 0.75rem 1rem 1rem;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.15rem;
  font-size: 0.8rem;
}
.powered-by {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.35rem;
}
.powered-by img {
  flex: none;
}
.content {
  padding: 1.25rem;
  max-inline-size: 90rem;
  inline-size: 100%;
  box-sizing: border-box;
  margin-inline: auto;
}
@media (max-width: 900px) {
  .sidebar {
    display: none;
  }
  .menu-btn {
    display: inline-flex;
  }
  .content {
    padding: 1rem;
  }
  .role {
    display: none;
  }
}
/* on a phone the chip keeps only its key icon */
@media (max-width: 600px) {
  .license-chip :deep(.p-tag-label) {
    display: none;
  }
}
</style>
