// The Supplier page's types and small rules (internal/core/web/supplier.go). Core checks everything again.

export interface SupplierStatus {
  available: boolean
  configured: boolean
  on: boolean
}

export type PriceMode = 'auto' | 'fixed'

export interface SupplierProduct {
  id: string
  name: string
  description: string
  /** the supplier's own category ("" when it names none) */
  category?: string
  cost_micro: number | null
  cost_currency: string
  stock: number | null
  listed: boolean
  enabled: boolean
  sell_price: number | null
  /** "auto": the price follows the USDT rate; "fixed": the owner's own */
  price_mode: PriceMode
  /** the supplier's cost is known in USDT, so the price can follow the rate */
  can_follow: boolean
  title: Record<string, string>
  desc: Record<string, string>
  sort: number
  synced_at: number | null
}

/** GET/PUT /supplier/pricing: the owner's switch and markup, the rate the store charges now, and how the products are priced. */
export interface SupplierPricing {
  auto: boolean
  /** percent on top of the supplier's cost, 0 to max_markup */
  markup: number
  max_markup: number
  /** an automatic price is rounded up to this many Toman */
  step: number
  /** as GET /usdt-rate answers: `rate` is Toman per USDT now (0: none known), `source` where it comes from */
  usdt: {
    auto: boolean
    margin: number
    manual: number
    rate: number
    source: '' | 'live' | 'manual'
    live: { rate: number; at: number; sources: string[]; fresh: boolean } | null
  }
  /** the rate the prices were last worked out at, and when (unix seconds); 0 when never */
  applied_rate: number
  applied_at: number
  counts: { auto: number; fixed: number; hand_priced: number; cannot_follow: number }
  /** only in the answer of a change: how many products got a new price, were made to follow, and had a typed price replaced */
  changed?: number
  switched?: number
  replaced?: number
}

export interface SupplierOrder {
  id: string
  status: 'paid' | 'done' | 'refunded'
  product: string
  product_id: string
  quantity: number
  amount: { amount: number; currency: string }
  attempts: number
  last_error: string
  created_at: number
  delivered_at: number | null
  buyer: { id: string; telegram_id: number; username: string } | null
}

/** GET /supplier/balance: the balance at the supplier; `raw` is its answer when no amount could be read from it. */
export interface SupplierBalanceAnswer {
  amount: string
  currency: string
  raw?: string
}

/** The balance as one line ("5.53 USDT"); empty when it could not be read. */
export function balanceLine(b: SupplierBalanceAnswer | null): string {
  return b && b.amount ? `${b.amount} ${b.currency}` : ''
}

/** How often an open page reads the balance again. */
export const balanceRefreshMs = 120_000

/** The most core takes for a price in Toman. */
export const maxPrice = 1_000_000_000_000

/** priceOk: a whole number of Toman from 1 to maxPrice (digits only; Persian and Arabic digits are read as Latin). */
export function parsePrice(s: string): number | null {
  const latin = s
    .replace(/[۰-۹]/g, (d) => String('۰۱۲۳۴۵۶۷۸۹'.indexOf(d)))
    .replace(/[٠-٩]/g, (d) => String('٠١٢٣٤٥٦٧٨٩'.indexOf(d)))
    .replace(/[,٬\s]/g, '')
  if (!/^\d{1,13}$/.test(latin)) return null
  const n = Number(latin)
  return n >= 1 && n <= maxPrice ? n : null
}

/** parseMarkup reads the markup in percent: a whole number from 0 to max (digits only; Persian and Arabic digits are read as Latin). */
export function parseMarkup(s: string, max: number): number | null {
  const latin = s
    .replace(/[۰-۹]/g, (d) => String('۰۱۲۳۴۵۶۷۸۹'.indexOf(d)))
    .replace(/[٠-٩]/g, (d) => String('٠١٢٣٤٥٦٧٨٩'.indexOf(d)))
    .trim()
  if (!/^\d{1,3}$/.test(latin)) return null
  const n = Number(latin)
  return n >= 0 && n <= max ? n : null
}

/**
 * autoPrice is the price one unit follows the rate to (core's SupplierAutoPrice): cost x rate x (100 + markup) / 100, rounded UP to the
 * next `step` Toman. null when it cannot be worked out. Exact (BigInt), as core is.
 */
export function autoPrice(costMicro: number | null, rate: number, markup: number, step = 1000): number | null {
  if (costMicro === null || costMicro <= 0 || rate <= 0 || markup < 0 || !Number.isInteger(costMicro) || !Number.isInteger(rate) || !Number.isInteger(markup)) return null
  const n = BigInt(costMicro) * BigInt(rate) * BigInt(100 + markup)
  const d = 100_000_000n * BigInt(step)
  const price = ((n + d - 1n) / d) * BigInt(step)
  return price > 0n && price <= BigInt(maxPrice) ? Number(price) : null
}

/** How old the newest catalog read may be before opening the page reads it again. */
export const staleCatalogMs = 10 * 60_000

/** catalogIsStale: no product was ever read, or the newest read is older than staleCatalogMs (now in milliseconds). */
export function catalogIsStale(products: Pick<SupplierProduct, 'synced_at'>[], now: number): boolean {
  const newest = Math.max(0, ...products.map((p) => p.synced_at ?? 0))
  return newest === 0 || now - newest * 1000 > staleCatalogMs
}

/** needsText: no Persian name or description yet, so the customer reads the supplier's English. */
export function needsText(p: Pick<SupplierProduct, 'title' | 'desc'>): boolean {
  return !(p.title.fa ?? '').trim() || !(p.desc.fa ?? '').trim()
}

export type ProductFilter = 'all' | 'sale' | 'unlisted' | 'needs_text'
export const productFilters: ProductFilter[] = ['all', 'sale', 'unlisted', 'needs_text']

export function filterProducts(list: SupplierProduct[], f: ProductFilter): SupplierProduct[] {
  switch (f) {
    case 'sale':
      return list.filter((p) => p.enabled)
    case 'unlisted':
      return list.filter((p) => !p.listed)
    case 'needs_text':
      return list.filter((p) => p.listed && needsText(p))
    default:
      return list
  }
}

/** costText writes a wholesale cost (millionths) like "2.6 USDT", or "" when the supplier did not say. */
export function costText(micro: number | null, currency: string): string {
  if (micro === null) return ''
  const whole = micro / 1_000_000
  return `${Number.isInteger(whole) ? whole : whole.toFixed(micro % 10_000 === 0 ? 2 : 6).replace(/0+$/, '')} ${currency}`
}

export function orderSeverity(s: string): 'success' | 'warn' | 'danger' | 'secondary' {
  return s === 'done' ? 'success' : s === 'paid' ? 'warn' : s === 'refunded' ? 'danger' : 'secondary'
}

export const orderStatuses = ['paid', 'done', 'refunded'] as const

/** sellableProblem: why a product cannot be switched on yet ('' when it can). */
export function sellableProblem(p: Pick<SupplierProduct, 'sell_price' | 'listed'>): '' | 'price' | 'unlisted' {
  if (p.sell_price === null) return 'price'
  return p.listed ? '' : 'unlisted'
}

/** The texts of the products as a file the owner can hand to a translator and bring back (exportTexts / parseTextImport). */
export interface TextRow {
  id: string
  category: string
  name: string
  supplier_description: string
  /** the supplier's cost and whether it still lists the product, to tell the products apart */
  cost: string
  listed: boolean
  /** no Persian name or description yet */
  needs_translation: boolean
  title: { fa: string; en: string }
  desc: { fa: string; en: string }
}

/** What the translator is told (the import ignores it): how the texts should read. */
export const textStyleGuide =
  'Fill title.fa/desc.fa (natural Persian) and title.en/desc.en (BOBRES brand English) for every product with needs_translation = true. ' +
  'Names read like "Claude API Credit $100 — 30 Days": product, plan or size, duration, with one leading emoji. Descriptions are short ' +
  'lines with a leading emoji each. Keep every URL, code, number, price and instruction exactly as the supplier wrote it; never name the supplier. ' +
  'Leave a text empty to keep the supplier\'s own.'

export function exportTexts(products: SupplierProduct[]): string {
  const rows: TextRow[] = products.map((p) => ({
    id: p.id,
    category: p.category ?? '',
    name: p.name,
    supplier_description: p.description,
    cost: costText(p.cost_micro, p.cost_currency),
    listed: p.listed,
    needs_translation: needsText(p),
    title: { fa: p.title.fa ?? '', en: p.title.en ?? '' },
    desc: { fa: p.desc.fa ?? '', en: p.desc.en ?? '' },
  }))
  return JSON.stringify({ style_guide: textStyleGuide, products: rows }, null, 2)
}

/** One product's edit from an imported file: only the texts that were given (an empty one is left alone). */
export interface TextEdit {
  id: string
  title?: Record<string, string>
  desc?: Record<string, string>
}

/** parseTextImport reads a file made by exportTexts (a translator may have filled the Persian texts in). Anything else is an error. */
export function parseTextImport(text: string, known: Set<string>): { edits: TextEdit[]; skipped: number } | { error: string } {
  let data: unknown
  try {
    data = JSON.parse(text)
  } catch {
    return { error: 'json' }
  }
  const list = (data as { products?: unknown })?.products
  if (!Array.isArray(list)) return { error: 'shape' }
  const edits: TextEdit[] = []
  let skipped = 0
  const pick = (v: unknown): Record<string, string> | undefined => {
    if (!v || typeof v !== 'object') return undefined
    const out: Record<string, string> = {}
    for (const lang of ['fa', 'en']) {
      const s = (v as Record<string, unknown>)[lang]
      if (typeof s === 'string' && s.trim() !== '') out[lang] = s.trim()
    }
    return Object.keys(out).length ? out : undefined
  }
  for (const row of list) {
    const id = (row as { id?: unknown })?.id
    if (typeof id !== 'string' || !known.has(id)) {
      skipped++
      continue
    }
    const title = pick((row as { title?: unknown }).title)
    const desc = pick((row as { desc?: unknown }).desc)
    if (title || desc) edits.push({ id, ...(title ? { title } : {}), ...(desc ? { desc } : {}) })
    else skipped++
  }
  return { edits, skipped }
}
