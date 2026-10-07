<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Drawer from 'primevue/drawer'
import Menu from 'primevue/menu'
import { useAuth } from '../stores/auth'
import { useUi } from '../stores/ui'
import { groups, visibleSections } from '../sections'
import { setLang, currentLang } from '../i18n'

const { t } = useI18n()
const auth = useAuth()
const ui = useUi()
const route = useRoute()
const router = useRouter()

const logo = import.meta.env.BASE_URL + 'favicon.svg'
const drawer = ref(false)
watch(() => route.fullPath, () => (drawer.value = false))

const nav = computed(() => {
  const list = visibleSections(auth.me?.permissions ?? [])
  return groups
    .map((g) => ({ group: g, items: list.filter((s) => s.group === g) }))
    .filter((g) => g.items.length > 0)
})

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

function toggleLang() {
  setLang(currentLang() === 'fa' ? 'en' : 'fa')
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
          <RouterLink v-for="s in g.items" :key="s.key" :to="{ name: s.key }" class="item" active-class="" exact-active-class="active">
            <i :class="s.icon" aria-hidden="true" />
            <span>{{ t('section.' + s.key + '.title') }}</span>
          </RouterLink>
        </template>
      </nav>
    </aside>

    <Drawer v-model:visible="drawer" :position="currentLang() === 'fa' ? 'right' : 'left'" class="mobile-nav" :header="auth.brand">
      <nav>
        <template v-for="g in nav" :key="g.group">
          <div class="group">{{ t('nav.group.' + g.group) }}</div>
          <RouterLink v-for="s in g.items" :key="s.key" :to="{ name: s.key }" class="item" exact-active-class="active">
            <i :class="s.icon" aria-hidden="true" />
            <span>{{ t('section.' + s.key + '.title') }}</span>
          </RouterLink>
        </template>
      </nav>
    </Drawer>

    <div class="main">
      <header class="topbar">
        <Button class="menu-btn" icon="pi pi-bars" text rounded :aria-label="t('nav.menu')" @click="drawer = true" />
        <span class="spacer" />
        <Button :label="t('lang.switch')" text size="small" @click="toggleLang" />
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
</style>
