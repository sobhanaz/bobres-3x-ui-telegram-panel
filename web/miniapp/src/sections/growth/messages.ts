// The texts of the growth tools that live inside the shop's own screens: the cashback on the wallet and on the result of a purchase, and the
// names of the wallet movements they write. (The wheel of fortune and the reseller's monthly target have their own sections.) They live here, like
// a section's, so the shop's table stays as it is; a test checks every text is in both languages.
export default {
  fa: {
    'ledger.cashback': 'کش‌بک',
    'ledger.cashback_reversal': 'کسر کش‌بک (برگشت سفارش)',
    'ledger.target_bonus': 'پاداش هدف ماهانه',
    'ledger.wheel_prize': 'جایزهٔ گردونهٔ شانس',

    'cashback.on': 'با هر خرید، {percent}٪ از مبلغ آن به‌صورت کش‌بک به کیف پولتان برمی‌گردد.',
    'cashback.min': 'برای سفارش‌های دست‌کم {amount}.',
    'cashback.max': 'سقف کش‌بک هر سفارش: {amount}.',
    'cashback.new_only': 'فقط برای سرویس جدید؛ تمدید کش‌بک ندارد.',
    'cashback.earned': 'کش‌بک دریافتی تا امروز: {amount}',
    'done.cashback': 'کش‌بک {amount} به کیف پولتان اضافه شد.',
  },
  en: {
    'ledger.cashback': 'Cashback',
    'ledger.cashback_reversal': 'Cashback taken back',
    'ledger.target_bonus': 'Monthly target bonus',
    'ledger.wheel_prize': 'Wheel of fortune prize',

    'cashback.on': 'Every purchase gives {percent}% of its price back to your wallet as cashback.',
    'cashback.min': 'For orders of at least {amount}.',
    'cashback.max': 'The most one order earns: {amount}.',
    'cashback.new_only': 'New services only; renewals do not earn it.',
    'cashback.earned': 'Cashback earned so far: {amount}',
    'done.cashback': 'You earned {amount} cashback. It was added to your wallet.',
  },
}
