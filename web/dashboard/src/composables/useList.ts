import { reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, query } from '../api'
import { errorText } from '../errors'
import type { Page } from '../types'

/** useList keeps one page of a server list (core's ?page=&size= lists) in
 * step with its filters: a filter change goes back to page 1, and typing
 * waits a moment before asking. Answers to older requests are dropped. */
export function useList<T, F extends Record<string, string>>(path: string, filters: F, size = 25) {
  const { t } = useI18n()
  const items = ref<T[]>([]) as { value: T[] }
  const total = ref(0)
  const page = ref(1)
  const rows = ref(size)
  const loading = ref(false)
  const error = ref('')
  const f = reactive({ ...filters }) as F
  let seq = 0

  async function load() {
    const mine = ++seq
    loading.value = true
    error.value = ''
    try {
      const res = await api<Page<T>>('GET', path + query({ ...f, page: page.value, size: rows.value }))
      if (mine !== seq) return
      items.value = res.items
      total.value = res.total
    } catch (e) {
      if (mine === seq) error.value = errorText(e, t).text
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  let timer: ReturnType<typeof setTimeout> | undefined
  watch(
    () => ({ ...f }),
    () => {
      page.value = 1
      clearTimeout(timer)
      timer = setTimeout(load, 300)
    },
  )

  /** onPage takes a PrimeVue DataTable page event. */
  function onPage(e: { page: number; rows: number }) {
    page.value = e.page + 1
    rows.value = e.rows
    void load()
  }

  return { items, total, page, rows, loading, error, filters: f, load, onPage }
}
