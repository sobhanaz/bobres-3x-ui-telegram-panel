<script setup lang="ts">
// The reseller's customer services: groups with their counts, a search by
// name or number, pages, and each service opened for its actions. Where the
// reseller is (group, search, page, the open service) is kept in ./state so a
// trip to the checkout and back finds the list as it was.
import { onMounted, ref } from 'vue'
import type { Service } from '../../api'
import { fmtNum } from '../../fmt'
import { tr } from '../../i18n'
import { fail } from '../../store'
import { errText, filters, resellerApi, type Filter, type ServicePage } from './api'
import Pager from './Pager.vue'
import ServiceCard from './ServiceCard.vue'
import { ui } from './state'

const emit = defineEmits<{ renew: [s: Service]; topup: [s: Service] }>()
const data = ref<ServicePage | null>(null)
const error = ref('')
const done = ref('')
const loading = ref(false)
const draft = ref(ui.query)
let latest = 0

async function load() {
  const mine = ++latest
  error.value = ''
  loading.value = true
  try {
    const page = await resellerApi.services({ status: ui.filter, q: ui.query, page: ui.page })
    if (mine !== latest) return // a newer request is on its way
    if (!page.services.length && ui.page > 1 && page.total > 0) { // past the end (services ended meanwhile)
      ui.page = 1
      await load()
      return
    }
    data.value = page
  } catch (e) {
    if (mine === latest && !fail(e)) error.value = errText(e)
  } finally {
    if (mine === latest) loading.value = false
  }
}
onMounted(load)

function group(f: Filter) {
  ui.filter = f
  ui.page = 1
  ui.open = ''
  done.value = ''
  void load()
}

function search() {
  ui.query = draft.value.trim()
  ui.page = 1
  ui.open = ''
  done.value = ''
  void load()
}

function clearSearch() {
  draft.value = ''
  search()
}

function go(page: number) {
  ui.page = page
  ui.open = ''
  void load()
}

/** changed: a service was changed (or is gone): say so and read the list again. */
function changed(message: string) {
  done.value = message ? tr(message) : ''
  void load()
}

const count = (f: Filter): number => {
  const c = data.value?.counts
  if (!c) return 0
  return f === '' ? c.total : c[f]
}
const pages = () => Math.max(1, Math.ceil((data.value?.total ?? 0) / (data.value?.size || 1)))
</script>

<template>
  <div class="rs-grid">
    <h2>{{ tr('reseller.services.title') }}</h2>

    <div class="rs-chips" role="group" :aria-label="tr('reseller.services.title')">
      <button v-for="f in filters" :key="f" class="rs-chip" type="button" :aria-pressed="ui.filter === f" @click="group(f)">
        {{ tr('reseller.filter.' + (f || 'all')) }} · {{ fmtNum(count(f)) }}
      </button>
    </div>

    <form class="row" role="search" @submit.prevent="search">
      <input v-model="draft" class="rs-search" type="search" maxlength="2000" autocomplete="off" enterkeyhint="search" :aria-label="tr('reseller.services.search')" :placeholder="tr('reseller.services.search')" />
      <button class="btn secondary" type="submit">{{ tr('reseller.services.search_go') }}</button>
    </form>
    <p v-if="ui.query" class="muted small">
      “{{ ui.query }}” <button class="link" type="button" @click="clearSearch">{{ tr('reseller.services.clear') }}</button>
    </p>

    <p v-if="done" class="ok" role="status">{{ done }}</p>
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>
    <p v-else-if="!data" class="muted">{{ tr('common.loading') }}</p>
    <p v-else-if="data.counts.total === 0" class="muted">{{ tr('reseller.services.empty') }}</p>
    <p v-else-if="data.services.length === 0" class="muted">{{ tr('reseller.services.none') }}</p>
    <ul v-else class="list">
      <ServiceCard
        v-for="s in data.services"
        :key="s.id"
        :service="s"
        :open="ui.open === s.id"
        @toggle="ui.open = ui.open === s.id ? '' : s.id"
        @renew="emit('renew', s)"
        @topup="emit('topup', s)"
        @changed="changed"
      />
    </ul>

    <Pager v-if="data" :page="data.page" :pages="pages()" :busy="loading" @go="go" />
  </div>
</template>
