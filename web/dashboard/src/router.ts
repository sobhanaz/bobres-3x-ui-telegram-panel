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
  users: () => import('./pages/UsersPage.vue'),
  services: () => import('./pages/ServicesPage.vue'),
  plans: () => import('./pages/PlansPage.vue'),
  payments: () => import('./pages/PaymentsPage.vue'),
  ledger: () => import('./pages/LedgerPage.vue'),
  discounts: () => import('./pages/DiscountsPage.vue'),
  branding: () => import('./pages/BrandingPage.vue'),
  settings: () => import('./pages/SettingsPage.vue'),
  staff: () => import('./pages/StaffPage.vue'),
  audit: () => import('./pages/AuditPage.vue'),
}

// Item pages belong to their section (menu highlight, permission).
const itemRoutes: RouteRecordRaw[] = [
  { path: 'users/:id', name: 'user', component: () => import('./pages/UserPage.vue'), meta: { perm: 'users.read', section: 'users' } },
  { path: 'services/:id', name: 'service', component: () => import('./pages/ServicePage.vue'), meta: { perm: 'services.read', section: 'services' } },
  { path: 'payments/orders/:id', name: 'order', component: () => import('./pages/OrderPage.vue'), meta: { perm: 'payments.read', section: 'payments' } },
]

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
      children: [...sectionRoutes, ...itemRoutes, { path: 'account', name: 'account', component: AccountPage }],
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
