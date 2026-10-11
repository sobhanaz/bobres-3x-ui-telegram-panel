import { createApp, watch } from 'vue'
import { createPinia } from 'pinia'
import PrimeVue from 'primevue/config'
import ToastService from 'primevue/toastservice'
import ConfirmationService from 'primevue/confirmationservice'
import Tooltip from 'primevue/tooltip'

import '@fontsource/vazirmatn/400.css'
import '@fontsource/vazirmatn/500.css'
import '@fontsource/vazirmatn/700.css'
import '@fontsource/inter/400.css'
import '@fontsource/inter/500.css'
import '@fontsource/inter/700.css'
import 'primeicons/primeicons.css'
import './styles.css'

import App from './App.vue'
import { router } from './router'
import { i18n, applyLang, currentLang } from './i18n'
import { BobresPreset, darkModeSelector } from './theme'
import { primeLocale } from './primelocale'
import type { Lang } from './format'
import { whenAuthLost } from './api'
import { useAuth } from './stores/auth'

applyLang(currentLang())

const app = createApp(App)
const pinia = createPinia()
app.use(pinia)
app.use(i18n)
app.use(router)
app.use(PrimeVue, {
  theme: { preset: BobresPreset, options: { darkModeSelector, cssLayer: false } },
  ripple: false,
  locale: primeLocale(currentLang()),
})
// PrimeVue's own words follow the dashboard language.
watch(i18n.global.locale, (lang) => {
  app.config.globalProperties.$primevue.config.locale = primeLocale(lang as Lang)
})
app.use(ToastService)
app.use(ConfirmationService)
app.directive('tooltip', Tooltip)

// A session that ends while the dashboard is open sends staff to the login page.
// The first check, before anyone logged in, is not that: redirecting then would
// drop a login link's token from the address bar before the login page reads it.
whenAuthLost(() => {
  const auth = useAuth(pinia)
  if (!auth.me) return
  auth.set(null)
  if (router.currentRoute.value.name !== 'login') void router.push({ name: 'login' })
})

app.mount('#app')
