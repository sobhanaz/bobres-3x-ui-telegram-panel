import { describe, expect, it } from 'vitest'
import { balanceLine, balanceRefreshMs, costText, exportTexts, maxPrice, orderSeverity, parsePrice, parseTextImport, sellableProblem, type SupplierProduct } from '../supplier'
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
    listed: true, enabled: true, sell_price: 90000, title: {}, desc: {}, sort: 0, synced_at: null, ...over,
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
