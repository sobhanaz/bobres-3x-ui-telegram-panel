import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuth } from './stores/auth'
import { sections } from './sections'
import AppLayout from './layouts/AppLayout.vue'

// Pages load on demand, so the first screen downloads only what it shows.
const LoginPage = () => import('./pages/LoginPage.vue')
const AccountPage = () => import('./pages/AccountPage.vue')
const SectionPlaceholder = () => import('./pages/SectionPlaceholder.vue')

// Sections built so far get their page; the others show what is coming.
const built: Record<string, RouteRecordRaw['component']> = {
  overview: () => import('./pages/OverviewPage.vue'),
}

const sectionRoutes: RouteRecordRaw[] = sections.map((s) => ({
  path: s.path,
  name: s.key,
  component: built[s.key] ?? SectionPlaceholder,
  meta: { perm: s.perm, section: s.key },
}))

export const router = createRouter({
  history: createWebHistory('/admin/'),
  routes: [
    { path: '/login', name: 'login', component: LoginPage, meta: { public: true } },
    {
      path: '/',
      component: AppLayout,
      children: [...sectionRoutes, { path: 'account', name: 'account', component: AccountPage }],
    },
    { path: '/:rest(.*)*', redirect: { name: 'overview' } },
  ],
})

router.beforeEach(async (to, from) => {
  const auth = useAuth()
  if (!auth.loaded) await auth.load()
  if (to.meta.public) {
    // A login link always goes through the login page, even when logged in,
    // and a failed one stays there to show why.
    if (auth.me && to.name === 'login' && from.name !== 'login' && !to.hash.startsWith('#t=')) return { name: 'overview' }
    return true
  }
  if (!auth.me) return { name: 'login' }
  const perm = to.meta.perm as string | undefined
  if (perm && !auth.can(perm)) return { name: 'overview' }
  return true
})
