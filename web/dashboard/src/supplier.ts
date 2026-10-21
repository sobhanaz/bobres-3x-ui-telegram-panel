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
