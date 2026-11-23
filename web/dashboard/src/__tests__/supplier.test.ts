import { describe, expect, it } from 'vitest'
import {
  autoPrice,
  balanceLine,
  balanceRefreshMs,
  catalogIsStale,
  costText,
  exportTexts,
  filterProducts,
  maxPrice,
  needsText,
  orderSeverity,
  parseMarkup,
  parsePrice,
  parseTextImport,
  productFilters,
  sellableProblem,
  staleCatalogMs,
  textStyleGuide,
  type SupplierProduct,
} from '../supplier'
import { keyPattern } from '../supplierkey'
import fa from '../locales/fa.json'
import en from '../locales/en.json'

describe('the Digital accounts page rules', () => {
  it('reads a price in Toman: whole, positive, at most core\'s limit, in any digits', () => {
    expect(parsePrice('450000')).toBe(450000)
    expect(parsePrice('450,000')).toBe(450000)
    expect(parsePrice(' ۴۵۰٬۰۰۰ ')).toBe(450000)
    expect(parsePrice('٤٥٠٠٠٠')).toBe(450000)
    expect(parsePrice(String(maxPrice))).toBe(maxPrice)
    for (const bad of ['', '0', '-5', '1.5', '12a', String(maxPrice + 1), '9'.repeat(14)]) expect(parsePrice(bad), bad).toBeNull()
  })
  it('writes the supplier\'s cost', () => {
    expect(costText(2_600_000, 'USDT')).toBe('2.6 USDT')
    expect(costText(4_000_000, 'USDT')).toBe('4 USDT')
    expect(costText(1_234_567, 'USDT')).toBe('1.234567 USDT')
    expect(costText(null, 'USDT')).toBe('')
  })
  it('takes an API key as core does', () => {
    expect(keyPattern.test('supkey-xxxxxxxxxxxxxxxx')).toBe(true)
    for (const bad of ['', 'short', 'has space 12345', 'line\nbreak1234', 'x'.repeat(301)]) expect(keyPattern.test(bad), bad).toBe(false)
  })
  it('says why a product cannot be switched on', () => {
    expect(sellableProblem({ sell_price: null, listed: true })).toBe('price')
    expect(sellableProblem({ sell_price: 1, listed: false })).toBe('unlisted')
    expect(sellableProblem({ sell_price: 1, listed: true })).toBe('')
    expect(orderSeverity('paid')).toBe('warn')
    expect(orderSeverity('done')).toBe('success')
  })
  it('has its texts in both languages', () => {
    const keys = ['intro', 'connection', 'api_key', 'turn_on', 'tab_products', 'tab_orders', 'sync', 'price', 'sell', 'refund', 'refund_help', 'no_orders']
    for (const cat of [fa, en] as Record<string, any>[]) {
      for (const k of keys) expect(cat.supplier[k], k).toBeTruthy()
      for (const s of ['paid', 'done', 'refunded']) expect(cat.supplier.order_status[s], s).toBeTruthy()
      expect(cat.section.supplier.title && cat.section.supplier.desc).toBeTruthy()
    }
  })
  it('writes the balance at the supplier, or nothing when it could not be read', () => {
    expect(balanceLine({ amount: '5.53', currency: 'USDT' })).toBe('5.53 USDT')
    expect(balanceLine({ amount: '', currency: '', raw: '{"status":"ok"}' })).toBe('')
    expect(balanceLine(null)).toBe('')
    // read again every couple of minutes while a page is open, never in a tight loop
    expect(balanceRefreshMs).toBeGreaterThanOrEqual(60_000)
  })
  it('has the balance card\'s texts in both languages', () => {
    for (const cat of [fa, en] as Record<string, any>[]) {
      for (const k of ['balance_title', 'balance_at', 'balance_refresh', 'balance_unknown', 'balance_raw']) expect(cat.supplier[k], k).toBeTruthy()
    }
  })
})

describe('exporting and importing the product texts', () => {
  const product = (id: string, over: Partial<SupplierProduct> = {}): SupplierProduct => ({
    id, name: 'Muse AI', description: 'A ready-made account', category: 'Muse AI', cost_micro: 250000, cost_currency: 'USDT', stock: 88,
    listed: true, enabled: true, sell_price: 90000, price_mode: 'fixed', can_follow: true, title: {}, desc: {}, sort: 0, synced_at: null, ...over,
  })
  it('writes every product with its names and texts, for a translator', () => {
    const rows = JSON.parse(exportTexts([product('a'), product('b', { title: { en: 'B' }, desc: { fa: 'ب' } })])).products
    expect(rows).toHaveLength(2)
    expect(rows[0]).toMatchObject({ id: 'a', category: 'Muse AI', name: 'Muse AI', supplier_description: 'A ready-made account', title: { fa: '', en: '' } })
    expect(rows[1]).toMatchObject({ title: { fa: '', en: 'B' }, desc: { fa: 'ب', en: '' } })
  })
  it('reads the file back: only the texts that were given, only known products', () => {
    const text = JSON.stringify({
      products: [
        { id: 'a', title: { fa: ' موز ', en: '' }, desc: { fa: 'توضیح' } },
        { id: 'b', title: { fa: '', en: '' }, desc: { fa: '', en: '' } },
        { id: 'zzz', title: { fa: 'x' } },
        { nope: 1 },
      ],
    })
    expect(parseTextImport(text, new Set(['a', 'b']))).toEqual({ edits: [{ id: 'a', title: { fa: 'موز' }, desc: { fa: 'توضیح' } }], skipped: 3 })
  })
  it('refuses what Export did not make', () => {
    expect(parseTextImport('not json', new Set())).toEqual({ error: 'json' })
    expect(parseTextImport('{"items":[]}', new Set())).toEqual({ error: 'shape' })
  })
  it('has its texts in both catalogs', () => {
    for (const cat of [fa, en] as Record<string, any>[]) {
      for (const k of ['category', 'copy_text', 'desc_help', 'export_texts', 'import_texts', 'texts_help', 'import_bad', 'import_done']) expect(typeof cat.supplier[k], k).toBe('string')
    }
  })
})

describe('pricing from the USDT price', () => {
  it('works the price out as core does: cost x rate x (100 + markup) / 100, rounded up to the next thousand', () => {
    expect(autoPrice(2_600_000, 140_000, 20)).toBe(437_000) // 436,800
    expect(autoPrice(4_000_000, 140_000, 20)).toBe(672_000) // exact
    expect(autoPrice(1_500_000, 100_001, 0)).toBe(151_000) // 150,001.5
    expect(autoPrice(1, 100_000, 0)).toBe(1_000) // a tiny cost is the first step
    expect(autoPrice(1_000_000, 100_000, 500)).toBe(600_000)
    expect(autoPrice(2_600_000, 140_000, 20, 500)).toBe(437_000)
    expect(autoPrice(2_600_000, 140_000, 20, 5000)).toBe(440_000)
  })
  it('does not guess: no cost, no rate, or a markup that is not a whole number gives no price', () => {
    for (const args of [[null, 100_000, 20], [0, 100_000, 20], [1_000_000, 0, 20], [1_000_000, 100_000, -1], [1_000_000, 100_000, 1.5], [1_000_000, 100_000.5, 20]] as const) {
      expect(autoPrice(args[0], args[1], args[2]), JSON.stringify(args)).toBeNull()
    }
    expect(autoPrice(Number.MAX_SAFE_INTEGER, 100_000_000, 500)).toBeNull() // over what core takes
  })
  it('reads the markup: a whole percent from 0 to the most core takes, in any digits', () => {
    expect(parseMarkup('20', 500)).toBe(20)
    expect(parseMarkup(' ۲۰ ', 500)).toBe(20)
    expect(parseMarkup('٢٥', 500)).toBe(25)
    expect(parseMarkup('0', 500)).toBe(0)
    expect(parseMarkup('500', 500)).toBe(500)
    for (const bad of ['', '-1', '501', '2.5', '1e2', 'abc', '1000']) expect(parseMarkup(bad, 500), bad).toBeNull()
  })
  it('reads the catalog again when the newest read is old (or there is none)', () => {
    const now = 1_800_000_000_000
    const at = (ms: number) => ({ synced_at: Math.floor((now - ms) / 1000) })
    expect(catalogIsStale([], now)).toBe(true)
    expect(catalogIsStale([{ synced_at: null }], now)).toBe(true)
    expect(catalogIsStale([at(60_000)], now)).toBe(false)
    expect(catalogIsStale([at(staleCatalogMs + 5_000), at(60_000)], now)).toBe(false) // the newest counts
    expect(catalogIsStale([at(staleCatalogMs + 5_000)], now)).toBe(true)
  })
})

describe('the list filter and the texts a product still needs', () => {
  const p = (id: string, over: Partial<SupplierProduct> = {}): SupplierProduct => ({
    id, name: id, description: 'x', cost_micro: 1_000_000, cost_currency: 'USDT', stock: 1, listed: true, enabled: false, sell_price: null,
    price_mode: 'auto', can_follow: true, title: {}, desc: {}, sort: 0, synced_at: null, ...over,
  })
  const done = { title: { fa: 'الف' }, desc: { fa: 'ب' } }
  const list = [p('a'), p('b', { enabled: true, ...done }), p('c', { listed: false }), p('d', { title: { fa: 'الف' }, desc: { en: 'only English' } })]
  it('says a product needs texts until it has a Persian name and description', () => {
    expect(needsText(list[0])).toBe(true)
    expect(needsText(list[1])).toBe(false)
    expect(needsText(list[3])).toBe(true)
    expect(needsText({ title: { fa: '  ' }, desc: { fa: 'x' } })).toBe(true)
  })
  it('filters: all, on sale, not in the catalog, needs Persian texts (a product the supplier dropped does not)', () => {
    expect(productFilters).toEqual(['all', 'sale', 'unlisted', 'needs_text'])
    expect(filterProducts(list, 'all')).toHaveLength(4)
    expect(filterProducts(list, 'sale').map((x) => x.id)).toEqual(['b'])
    expect(filterProducts(list, 'unlisted').map((x) => x.id)).toEqual(['c'])
    expect(filterProducts(list, 'needs_text').map((x) => x.id)).toEqual(['a', 'd'])
  })
  it('tells the translator in the export file which products need texts, and how they should read', () => {
    const out = JSON.parse(exportTexts(list))
    expect(out.style_guide).toBe(textStyleGuide)
    expect(textStyleGuide).toMatch(/URL/)
    expect(out.products.map((r: { id: string; needs_translation: boolean }) => [r.id, r.needs_translation])).toEqual([['a', true], ['b', false], ['c', true], ['d', true]])
    expect(out.products[0]).toMatchObject({ cost: '1 USDT', listed: true })
    expect(parseTextImport(JSON.stringify(out), new Set(['a']))).toEqual({ edits: [{ id: 'a', title: {}, desc: {} }].slice(1), skipped: 4 }) // an untouched export changes nothing
  })
  it('has the pricing texts in every catalog', async () => {
    const cats = { en: (await import('../locales/en.json')).default, fa: (await import('../locales/fa.json')).default, ar: (await import('../locales/ar.json')).default, ru: (await import('../locales/ru.json')).default, tr: (await import('../locales/tr.json')).default } as Record<string, any>
    const keys = ['pricing_title', 'rate_line', 'rate_source_live', 'rate_source_manual', 'rate_none', 'rate_refresh', 'pricing_auto', 'markup', 'markup_help', 'formula', 'pricing_save',
      'pricing_saved', 'apply_all', 'apply_help', 'apply_replaces', 'applied', 'counts', 'mode_auto', 'mode_fixed', 'follow', 'follow_help', 'follow_off_note', 'follow_no_rate',
      'follow_no_cost', 'formula_would', 'needs_text', 'filter', 'filter_all', 'filter_sale', 'filter_unlisted', 'filter_needs_text']
    for (const [lang, cat] of Object.entries(cats)) for (const k of keys) expect(typeof cat.supplier[k], `${lang}.${k}`).toBe('string')
  })
})
