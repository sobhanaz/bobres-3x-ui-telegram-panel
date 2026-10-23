// The Supplier page's types and small rules (internal/core/web/supplier.go). Core checks everything again.

export interface SupplierStatus {
  available: boolean
  configured: boolean
  on: boolean
}

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
  title: Record<string, string>
  desc: Record<string, string>
  sort: number
  synced_at: number | null
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
  title: { fa: string; en: string }
  desc: { fa: string; en: string }
}

export function exportTexts(products: SupplierProduct[]): string {
  const rows: TextRow[] = products.map((p) => ({
    id: p.id,
    category: p.category ?? '',
    name: p.name,
    supplier_description: p.description,
    title: { fa: p.title.fa ?? '', en: p.title.en ?? '' },
    desc: { fa: p.desc.fa ?? '', en: p.desc.en ?? '' },
  }))
  return JSON.stringify({ products: rows }, null, 2)
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
