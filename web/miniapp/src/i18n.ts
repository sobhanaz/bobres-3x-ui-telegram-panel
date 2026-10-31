// Persian (right to left) and English in one small table here, and Arabic
// (right to left), Russian and Turkish in beta (machine-translated, awaiting a
// native review) in src/locales/<lang>.json: {name} placeholders are replaced,
// and a plural selector {name|form|…} picks the form the number takes in the
// language (the bot's syntax, see internal/bot/i18n/plural.go); nothing else is
// interpreted.
import { ref, computed } from 'vue'
import { isLang, latinDigits, numberLocale, rtl, type Lang } from './format'
import ar from './locales/ar.json'
import ru from './locales/ru.json'
import trTexts from './locales/tr.json'

const fa = {
  'tab.store': 'فروشگاه',
  'tab.services': 'سرویس‌های من',
  'tab.wallet': 'کیف پول',
  'tab.more': 'بیشتر',
  'more.title': 'بیشتر',
  'common.back': 'بازگشت',
  'common.retry': 'تلاش دوباره',
  'common.loading': 'در حال بارگذاری…',
  'common.copy': 'کپی',
  'common.copied': 'کپی شد',
  'common.close': 'بستن',
  'common.open_bot': 'باز کردن ربات',
  'common.support': 'پشتیبانی',
  'gate.not_telegram': 'این فروشگاه را از داخل تلگرام باز کنید.',
  'gate.maintenance': 'فروشگاه در حال تعمیر است. کمی بعد برگردید.',
  'gate.banned': 'این حساب نمی‌تواند از فروشگاه استفاده کند.',
  'gate.join_required': 'برای استفاده از فروشگاه، ابتدا در کانال ما عضو شوید و سپس دکمه «عضو شدم» را بزنید.',
  'gate.expired': 'نشست شما تمام شد. فروشگاه را دوباره از تلگرام باز کنید.',
  'gate.unavailable': 'فروشگاه الان شلوغ است. کمی بعد دوباره تلاش کنید.',
  'gate.off': 'فروشگاه فعال نیست.',
  'join.channel': 'عضویت در کانال',
  'join.check': 'عضو شدم',
  'join.checking': 'در حال بررسی…',
  'join.not_yet': 'هنوز عضو کانال نشده‌اید. ابتدا عضو شوید و سپس دوباره «عضو شدم» را بزنید.',
  'gate.join_required_many': 'برای استفاده از فروشگاه، ابتدا در کانال‌های زیر عضو شوید و سپس دکمه «عضو شدم» را بزنید.',
  'join.chat': 'عضویت در {name}',
  'join.unnamed': 'کانال {n}',
  'join.not_yet_many': 'هنوز در همهٔ کانال‌ها عضو نشده‌اید. در بقیه عضو شوید و سپس دوباره «عضو شدم» را بزنید.',
  'plans.title': 'خرید سرویس',
  'plans.empty': 'فعلاً پلنی برای فروش نیست.',
  'plans.days': '{n} روز',
  'plans.traffic': '{size}',
  'plans.unlimited': 'نامحدود',
  'plans.topup': 'ترافیک اضافه',
  'plans.buy': 'خرید',
  'plans.was': 'قیمت اصلی {price}',
  'plans.choose_group': 'یک دسته را انتخاب کنید',
  'plans.other': 'سایر پلن‌ها',
  'plans.all_groups': 'همهٔ دسته‌ها',
  'services.title': 'سرویس‌های من',
  'services.empty': 'هنوز سرویسی ندارید.',
  'services.expires': 'پایان: {date}',
  'services.days_left': '{n} روز مانده',
  'services.expired': 'تمام شده',
  'services.traffic': '{used} از {total}',
  'services.traffic_free': '{used} مصرف شده',
  'services.renew': 'تمدید',
  'services.topup': 'ترافیک اضافه',
  'services.link': 'لینک اشتراک',
  'services.page': 'صفحه سرویس',
  'services.choose_plan': 'پلن را انتخاب کنید',
  'services.no_plans': 'پلنی برای این کار نیست.',
  'status.active': 'فعال',
  'status.expiring_soon': 'رو به پایان',
  'status.expired': 'منقضی',
  'status.depleted': 'ترافیک تمام شده',
  'status.disabled': 'غیرفعال',
  'status.pending': 'در حال آماده‌سازی',
  'wallet.title': 'کیف پول',
  'wallet.balance': 'موجودی',
  'wallet.usdt': 'معادل {usdt} · نرخ لحظه‌ای تتر: {rate}',
  'wallet.topup': 'افزایش موجودی',
  'wallet.history': 'آخرین تراکنش‌ها',
  'wallet.empty': 'تراکنشی نیست.',
  'wallet.amount': 'مبلغ ({currency})',
  'wallet.range': 'از {min} تا {max}',
  'wallet.custom': 'مبلغ دلخواه',
  'wallet.bad_amount': 'مبلغ معتبر نیست.',
  'ledger.topup': 'شارژ',
  'ledger.purchase': 'خرید',
  'ledger.refund': 'بازگشت وجه',
  'ledger.adjustment': 'اصلاح',
  'ledger.gift_code': 'کد هدیه',
  'ledger.license': 'لایسنس BOBRES',
  'ledger.digital': 'حساب دیجیتال',
  'ledger.affiliate': 'همکاری در فروش',
  'ledger.convert': 'تبدیل تتر به تومان',
  'ledger.other': 'تراکنش',
  'checkout.title': 'تأیید خرید',
  'checkout.summary_new': 'خرید {plan}',
  'checkout.summary_renew': 'تمدید با {plan}',
  'renew.note': 'نحوهٔ تمدید: حجم این پلن به حجم فعلی سرویس اضافه می‌شود، ولی زمان از نو شروع می‌شود؛ روزهای باقی‌مانده صفر می‌شود و مدت پلن از همین امروز حساب می‌شود.',
  'checkout.summary_topup': 'ترافیک اضافه {plan}',
  'checkout.discount': 'کد تخفیف',
  'checkout.apply': 'اعمال',
  'checkout.discount_applied': 'تخفیف {amount} اعمال شد',
  'checkout.discount_bad': 'کد تخفیف پذیرفته نشد.',
  'checkout.total': 'مبلغ قابل پرداخت',
  'checkout.continue': 'ادامه',
  'checkout.pay_wallet': 'پرداخت از کیف پول ({balance})',
  'checkout.pay_methods': 'پرداخت آنلاین',
  'checkout.low_wallet': 'موجودی کیف پول کافی نیست.',
  'checkout.no_methods': 'روش پرداخت آنلاین فعالی نیست. از ربات پرداخت کنید یا کیف پول را شارژ کنید.',
  'checkout.free': 'رایگان',
  'method.zarinpal': 'زرین‌پال',
  'method.idpay': 'آیدی‌پی',
  'method.nextpay': 'نکست‌پی',
  'method.aqayepardakht': 'آقای‌پرداخت',
  'method.usdt_trc20_auto': 'تتر (TRC20)',
  'method.ton_auto': 'تون (TON)',
  'pay.waiting': 'منتظر پرداخت…',
  'pay.open': 'باز کردن صفحه پرداخت',
  'pay.paid_check': 'پرداخت کردم',
  'pay.checking': 'در حال بررسی…',
  'pay.not_yet': 'هنوز پرداختی دیده نشد.',
  'pay.amount_charged': 'مبلغ: {amount}',
  'pay.usdt_send': 'دقیقاً {amount} را به این آدرس بفرستید:',
  'pay.usdt_network': 'شبکه {network}',
  'pay.usdt_until': 'تا {time} معتبر است.',
  'pay.ton_intro': 'از هر کیف پول تون، دقیقاً همین مقدار را به آدرس زیر بفرستید.',
  'pay.ton_amount': 'مبلغ دقیق',
  'pay.ton_address': 'آدرس مقصد',
  'pay.ton_memo': 'کامنت (Memo)',
  'pay.ton_memo_warning': 'این کامنت را حتماً در بخش «کامنت» انتقال بنویسید. بدون آن، پرداخت شما شناسایی نمی‌شود و فقط به‌صورت دستی بررسی می‌شود.',
  'pay.ton_time_left': 'زمان باقی‌مانده: {time}',
  'pay.ton_expired': 'مهلت پرداخت تمام شد. اگر دیرتر پرداخت کرده‌اید، فروشگاه آن را بررسی می‌کند.',
  'pay.ton_not_yet': 'هنوز پرداختی دیده نشد. رسیدن تراکنش تون ممکن است یکی دو دقیقه طول بکشد. اگر کامنت را ننوشته‌اید، پرداخت شناسایی نمی‌شود.',
  'pay.failed': 'پرداخت انجام نشد.',
  'done.title': 'انجام شد',
  'done.paid': 'پرداخت شد. سرویس در حال آماده‌سازی است…',
  'done.ready': 'سرویس شما آماده است.',
  'done.topup': 'موجودی کیف پول افزایش یافت.',
  'done.stuck': 'پرداخت ثبت شد ولی ساخت سرویس طول کشید. چند دقیقه بعد «سرویس‌های من» را ببینید یا به پشتیبانی پیام دهید.',
  'done.cancelled': 'این سفارش لغو یا منقضی شد.',
  'done.view': 'دیدن سرویس‌ها',
  'done.back_store': 'بازگشت به فروشگاه',
  'err.network': 'ارتباط برقرار نشد. اینترنت را بررسی کنید.',
  'err.rate_limited': 'درخواست‌ها زیاد است. یک دقیقه صبر کنید.',
  'err.insufficient_funds': 'موجودی کیف پول کافی نیست.',
  'err.plan_unavailable': 'این پلن دیگر در دسترس نیست.',
  'err.not_extendable': 'این سرویس را نمی‌شود تمدید کرد.',
  'err.not_payable': 'این سفارش دیگر قابل پرداخت نیست.',
  'err.unavailable_now': 'فروشگاه الان نمی‌تواند این را بفروشد.',
  'err.maintenance': 'فروشگاه در حال تعمیر است.',
  'err.banned': 'این حساب نمی‌تواند از فروشگاه استفاده کند.',
  'err.state': 'الان نمی‌شود این کار را انجام داد.',
  'err.invalid': 'ورودی معتبر نیست.',
  'err.unknown': 'مشکلی پیش آمد. دوباره تلاش کنید.',
} as const

const en: Record<keyof typeof fa, string> = {
  'tab.store': 'Store',
  'tab.services': 'My services',
  'tab.wallet': 'Wallet',
  'tab.more': 'More',
  'more.title': 'More',
  'common.back': 'Back',
  'common.retry': 'Try again',
  'common.loading': 'Loading…',
  'common.copy': 'Copy',
  'common.copied': 'Copied',
  'common.close': 'Close',
  'common.open_bot': 'Open the bot',
  'common.support': 'Support',
  'gate.not_telegram': 'Open this store from inside Telegram.',
  'gate.maintenance': 'The store is under maintenance. Come back in a little while.',
  'gate.banned': 'This account cannot use the store.',
  'gate.join_required': 'To use the store, join our channel first, then tap “I’ve joined”.',
  'gate.expired': 'Your session ended. Open the store again from Telegram.',
  'gate.unavailable': 'The store is busy. Try again in a minute.',
  'gate.off': 'The store is not available.',
  'join.channel': 'Join the channel',
  'join.check': 'I’ve joined',
  'join.checking': 'Checking…',
  'join.not_yet': 'You haven’t joined the channel yet. Join it first, then tap “I’ve joined” again.',
  'gate.join_required_many': 'To use the store, join the channels below first, then tap “I’ve joined”.',
  'join.chat': 'Join {name}',
  'join.unnamed': 'channel {n}',
  'join.not_yet_many': 'You haven’t joined all of them yet. Join the rest, then tap “I’ve joined” again.',
  'plans.title': 'Buy a service',
  'plans.empty': 'No plans for sale right now.',
  'plans.days': '{n} days',
  'plans.traffic': '{size}',
  'plans.unlimited': 'Unlimited',
  'plans.topup': 'Extra traffic',
  'plans.buy': 'Buy',
  'plans.was': 'List price {price}',
  'plans.choose_group': 'Choose a category',
  'plans.other': 'Other plans',
  'plans.all_groups': 'All categories',
  'services.title': 'My services',
  'services.empty': 'You have no services yet.',
  'services.expires': 'Ends {date}',
  'services.days_left': '{n} days left',
  'services.expired': 'Ended',
  'services.traffic': '{used} of {total}',
  'services.traffic_free': '{used} used',
  'services.renew': 'Renew',
  'services.topup': 'Add traffic',
  'services.link': 'Subscription link',
  'services.page': 'Service page',
  'services.choose_plan': 'Choose a plan',
  'services.no_plans': 'No plans for this.',
  'status.active': 'Active',
  'status.expiring_soon': 'Ending soon',
  'status.expired': 'Expired',
  'status.depleted': 'Out of traffic',
  'status.disabled': 'Disabled',
  'status.pending': 'Being set up',
  'wallet.title': 'Wallet',
  'wallet.balance': 'Balance',
  'wallet.usdt': 'About {usdt} · USDT rate now: {rate}',
  'wallet.topup': 'Add funds',
  'wallet.history': 'Latest movements',
  'wallet.empty': 'No movements yet.',
  'wallet.amount': 'Amount ({currency})',
  'wallet.range': 'From {min} to {max}',
  'wallet.custom': 'Other amount',
  'wallet.bad_amount': 'That amount is not valid.',
  'ledger.topup': 'Top-up',
  'ledger.purchase': 'Purchase',
  'ledger.refund': 'Refund',
  'ledger.adjustment': 'Adjustment',
  'ledger.gift_code': 'Gift code',
  'ledger.license': 'BOBRES license',
  'ledger.digital': 'Digital account',
  'ledger.affiliate': 'Affiliate program',
  'ledger.convert': 'USDT converted to Toman',
  'ledger.other': 'Movement',
  'checkout.title': 'Confirm purchase',
  'checkout.summary_new': 'Buy {plan}',
  'checkout.summary_renew': 'Renew with {plan}',
  'renew.note': 'How renewal works: the plan\'s traffic is added to the traffic the service has now, but the time starts over: the days left are dropped and the plan\'s period counts from today.',
  'checkout.summary_topup': 'Extra traffic: {plan}',
  'checkout.discount': 'Discount code',
  'checkout.apply': 'Apply',
  'checkout.discount_applied': 'Discount of {amount} applied',
  'checkout.discount_bad': 'That discount code was not accepted.',
  'checkout.total': 'To pay',
  'checkout.continue': 'Continue',
  'checkout.pay_wallet': 'Pay from the wallet ({balance})',
  'checkout.pay_methods': 'Pay online',
  'checkout.low_wallet': 'The wallet balance is too low.',
  'checkout.no_methods': 'No online payment method is on. Pay in the bot, or top up the wallet.',
  'checkout.free': 'Free',
  'method.zarinpal': 'Zarinpal',
  'method.idpay': 'IDPay',
  'method.nextpay': 'NextPay',
  'method.aqayepardakht': 'Aqayepardakht',
  'method.usdt_trc20_auto': 'USDT (TRC20)',
  'method.ton_auto': 'TON',
  'pay.waiting': 'Waiting for the payment…',
  'pay.open': 'Open the payment page',
  'pay.paid_check': 'I have paid',
  'pay.checking': 'Checking…',
  'pay.not_yet': 'No payment seen yet.',
  'pay.amount_charged': 'Amount: {amount}',
  'pay.usdt_send': 'Send exactly {amount} to this address:',
  'pay.usdt_network': 'Network {network}',
  'pay.usdt_until': 'Valid until {time}.',
  'pay.ton_intro': 'From any TON wallet, send exactly this amount to the address below.',
  'pay.ton_amount': 'Exact amount',
  'pay.ton_address': 'Address to pay',
  'pay.ton_memo': 'Comment (memo)',
  'pay.ton_memo_warning': 'You must write this comment in the transfer’s comment field. Without it your payment is not recognized and can only be handled by hand.',
  'pay.ton_time_left': 'Time left: {time}',
  'pay.ton_expired': 'The time to pay is over. A payment made late is still looked at by the store.',
  'pay.ton_not_yet': 'No payment seen yet. A TON transfer can take a minute or two to show up. If the comment was left out, the payment cannot be recognized.',
  'pay.failed': 'The payment did not go through.',
  'done.title': 'Done',
  'done.paid': 'Paid. Your service is being set up…',
  'done.ready': 'Your service is ready.',
  'done.topup': 'The wallet balance went up.',
  'done.stuck': 'The payment is recorded but setting up the service is taking long. Check My services in a few minutes, or message support.',
  'done.cancelled': 'This order was cancelled or expired.',
  'done.view': 'See my services',
  'done.back_store': 'Back to the store',
  'err.network': 'Could not connect. Check your internet.',
  'err.rate_limited': 'Too many requests. Wait a minute.',
  'err.insufficient_funds': 'The wallet balance is too low.',
  'err.plan_unavailable': 'This plan is no longer available.',
  'err.not_extendable': 'This service cannot be renewed.',
  'err.not_payable': 'This order can no longer be paid.',
  'err.unavailable_now': 'The store cannot sell this right now.',
  'err.maintenance': 'The store is under maintenance.',
  'err.banned': 'This account cannot use the store.',
  'err.state': 'This cannot be done right now.',
  'err.invalid': 'The input is not valid.',
  'err.unknown': 'Something went wrong. Try again.',
}

/** SectionMessages are a section's own texts (src/sections/<name>/messages.ts, its default export). A section may carry its
 * beta languages too; the ones it leaves out are read from src/locales/<lang>.json (a test checks every text is in all five). */
export interface SectionMessages {
  fa: Record<string, string>
  en: Record<string, string>
  ar?: Record<string, string>
  ru?: Record<string, string>
  tr?: Record<string, string>
}

/** Key is a text's name: the shop's are known to the compiler, a section's are plain strings (a test checks every one is in all five languages). */
export type Key = keyof typeof fa | (string & {})

// `en` is declared as Record<keyof typeof fa, string>: the compiler checks that every text of the shop is in both languages.
export const messages: Record<Lang, Record<string, string>> = { fa: { ...fa }, en: { ...en }, ar: { ...ar }, ru: { ...ru }, tr: { ...trTexts } }

// The sections add theirs, so a new section touches no shared file.
for (const t of Object.values(import.meta.glob<SectionMessages>('./sections/*/messages.ts', { eager: true, import: 'default' }))) {
  Object.assign(messages.fa, t.fa)
  Object.assign(messages.en, t.en)
  for (const l of ['ar', 'ru', 'tr'] as const) Object.assign(messages[l], t[l] ?? {})
}

export const lang = ref<Lang>('fa')
export const dir = computed(() => (rtl(lang.value) ? 'rtl' : 'ltr'))

/** pickLang maps a Telegram or stored language code to one of the languages the store enabled (the server sends them; Persian
 * and English until it has), else to the store's default, the first of them. */
export function pickLang(code: string | undefined, enabled: readonly string[] = ['fa', 'en']): Lang {
  const langs = enabled.filter(isLang)
  const c = (code ?? '').toLowerCase().trim()
  const hit = langs.find((l) => c === l || c.startsWith(l + '-'))
  return hit ?? langs[0] ?? 'fa'
}

export function setLang(l: Lang): void {
  lang.value = l
  document.documentElement.lang = l
  document.documentElement.dir = rtl(l) ? 'rtl' : 'ltr'
}

/** tr looks a message up in the current language and fills {placeholders}. */
export function tr(key: Key, params: Record<string, string | number> = {}): string {
  return translate(lang.value, key, params)
}

// The forms of a plural selector, in the order of the language's categories (CLDR).
const pluralForms: Record<Lang, Record<string, number>> = {
  fa: { one: 0, other: 1 },
  en: { one: 0, other: 1 },
  tr: { one: 0, other: 1 },
  ru: { one: 0, few: 1, many: 2, other: 1 }, // a fraction takes the "few" form: 1,5 дня
  ar: { zero: 0, one: 1, two: 2, few: 3, many: 4, other: 5 },
}

/** countOf reads a value back as a number: a number as it is, a text as the language writes numbers ("1,234", "۱٬۲۳۴",
 * "١٬٢٣٤", "1 234,5", "1.234,5"); NaN for anything else (an amount with its unit). */
export function countOf(v: string | number | undefined, l: Lang): number {
  if (typeof v === 'number') return v
  const decimal = l === 'ru' || l === 'tr' ? ',' : l === 'fa' || l === 'ar' ? '٫.' : '.'
  const group = l === 'ru' ? ' \u00a0\u202f' : l === 'tr' ? '.' : ',٬'
  let s = ''
  for (const ch of latinDigits(String(v ?? '').trim())) {
    if (/\d/.test(ch)) s += ch
    else if (decimal.includes(ch) && s && !s.includes('.')) s += '.'
    else if (group.includes(ch) && s && !s.includes('.')) continue
    else if (ch === '-' && !s) s += ch
    else return NaN
  }
  return s && s !== '-' ? Number(s) : NaN
}

export function translate(l: Lang, key: Key, params: Record<string, string | number> = {}): string {
  const text = messages[l][key] ?? messages.en[key] ?? key
  return text
    .replace(/\{(\w+)((?:\|[^{}|\n]*)+)\}/g, (_, k: string, rest: string) => {
      const forms = rest.slice(1).split('|')
      const n = countOf(params[k], l)
      const i = Number.isNaN(n) ? forms.length - 1 : Math.min(pluralForms[l][new Intl.PluralRules(numberLocale(l)).select(Math.abs(n))] ?? forms.length - 1, forms.length - 1)
      return forms[i].replaceAll('#', String(params[k] ?? ''))
    })
    .replace(/\{(\w+)\}/g, (_, k: string) => String(params[k] ?? ''))
}

/** planName picks a plan's name in the language, falling back to English and then any. */
export function planName(names: Record<string, string>, l: Lang): string {
  return names[l] || names.en || Object.values(names)[0] || ''
}
