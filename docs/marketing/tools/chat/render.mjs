// A small Telegram-like chat renderer: it draws the bot's REAL texts (internal/bot/i18n/locales/{fa,en}.json,
// demo values filled in) and the REAL button colors (the buttonStyles table in internal/bot/i18n/meta.go)
// as dark-theme chat screenshots, for the landing page and the launch posts.
//
//   node render.mjs <out-dir>            (needs puppeteer-core and Chrome, see ../README.md)
//
// These are illustrations: the bubbles, the keyboard and the QR code are drawn here, not captured from Telegram.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import puppeteer from 'puppeteer-core'

const here = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(here, '../../../..')
const out = path.resolve(process.argv[2] || path.join(here, 'out'))
const CHROME = process.env.CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
fs.mkdirSync(out, { recursive: true })

// ---- the bot's texts and button colors ---------------------------------------------------------
const cat = {
  fa: JSON.parse(fs.readFileSync(path.join(repo, 'internal/bot/i18n/locales/fa.json'), 'utf8')),
  en: JSON.parse(fs.readFileSync(path.join(repo, 'internal/bot/i18n/locales/en.json'), 'utf8')),
}
const meta = fs.readFileSync(path.join(repo, 'internal/bot/i18n/meta.go'), 'utf8')
const styleBlock = meta.slice(meta.indexOf('var buttonStyles'), meta.indexOf('// ButtonStyle is'))
const styles = {}
for (const m of styleBlock.matchAll(/"([a-z_.]+)":\s*"(success|primary|danger)"/g)) styles[m[1]] = m[2]

const T = (lang, key, args = {}) => {
  const raw = cat[lang][key]
  if (raw === undefined) throw new Error(`no text ${key} in ${lang}`)
  return raw.replace(/\{([a-z_]+)\}/g, (_, k) => {
    if (!(k in args)) throw new Error(`text ${key}: no value for {${k}}`)
    return args[k]
  })
}
// A button: its label comes from the key's text, its color from the style table.
const B = (lang, key, args = {}) => ({ text: T(lang, key, args), style: styles[key] || '' })

// ---- the bot's number, money, size and date formats (internal/bot/i18n/format.go) ----------------
const fa = (lang) => lang === 'fa'
const digits = (lang, s) => (fa(lang) ? String(s).replace(/\d/g, (d) => '۰۱۲۳۴۵۶۷۸۹'[d]).replace(/,/g, '٬').replace(/\./g, '٫') : String(s))
const group = (n) => String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
const money = (lang, n) => digits(lang, group(n)) + ' ' + (fa(lang) ? 'تومان' : 'Toman')
const bytes = (lang, gb) => (fa(lang) ? digits(lang, gb) + ' گیگابایت' : gb + ' GB')
const MONTHS = ['فروردین', 'اردیبهشت', 'خرداد', 'تیر', 'مرداد', 'شهریور', 'مهر', 'آبان', 'آذر', 'دی', 'بهمن', 'اسفند']
function toJalali(gy, gm, gd) {
  const gdm = [0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334]
  const gy2 = gm > 2 ? gy + 1 : gy
  let days = 355666 + 365 * gy + Math.floor((gy2 + 3) / 4) - Math.floor((gy2 + 99) / 100) + Math.floor((gy2 + 399) / 400) + gd + gdm[gm - 1]
  let jy = -1595 + 33 * Math.floor(days / 12053)
  days %= 12053
  jy += 4 * Math.floor(days / 1461)
  days %= 1461
  if (days > 365) {
    jy += Math.floor((days - 1) / 365)
    days = (days - 1) % 365
  }
  return days < 186 ? [jy, 1 + Math.floor(days / 31), 1 + (days % 31)] : [jy, 7 + Math.floor((days - 186) / 30), 1 + ((days - 186) % 30)]
}
const EN_MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
function date(lang, d) {
  if (!fa(lang)) return `${d.getUTCDate()} ${EN_MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`
  const [y, m, dd] = toJalali(d.getUTCFullYear(), d.getUTCMonth() + 1, d.getUTCDate())
  return `${digits(lang, dd)} ${MONTHS[m - 1]} ${digits(lang, y)}`
}
const clock = (lang, hhmm) => digits(lang, hhmm)

// ---- demo values: nothing here is real ------------------------------------------------------------
const BRAND = 'Demo VPN'
const PLANS = {
  fa: [['۱۰ گیگ · ۳۰ روزه', 60000, 30, 10], ['۳۰ گیگ · ۳۰ روزه', 120000, 30, 30], ['۱۰۰ گیگ · ۳۰ روزه', 260000, 30, 100], ['ترافیک نامحدود · ۳۰ روزه', 420000, 30, 0]],
  en: [['10 GB · 30 days', 60000, 30, 10], ['30 GB · 30 days', 120000, 30, 30], ['100 GB · 30 days', 260000, 30, 100], ['Unlimited traffic · 30 days', 420000, 30, 0]],
}
const LINK = 'https://sub.example.com/s/Xk3dFq9aVnT2'
const TODAY = new Date(Date.UTC(2026, 9, 14)) // 14 Oct 2026
const plusDays = (n) => new Date(TODAY.getTime() + n * 86400000)

// ---- the Telegram-HTML subset the bot uses (<b>, <code>) -------------------------------------------
const esc = (s) => s.replace(/&(?!lt;|gt;|amp;)/g, '&amp;')
const html = (s) => esc(s).replace(/\n/g, '<br>').replace(/<code>/g, '<code>').replace(/<\/code>/g, '</code>')

// ---- the page ---------------------------------------------------------------------------------------
const fonts = pathToFileURL(path.join(repo, 'site/assets/fonts')).href
const avatar = pathToFileURL(path.join(repo, 'docs/brand/bot-avatar.jpg')).href

const css = `
@font-face{font-family:Vazirmatn;font-weight:400;src:url(${fonts}/vazirmatn-arabic-400.woff2) format('woff2');unicode-range:U+0600-06FF,U+0750-077F,U+0870-088E,U+0890-0891,U+0897-08E1,U+08E3-08FF,U+200C-200E,U+2010-2011,U+204F,U+2E41,U+FB50-FDFF,U+FE70-FE74,U+FE76-FEFC}
@font-face{font-family:Vazirmatn;font-weight:600;src:url(${fonts}/vazirmatn-arabic-600.woff2) format('woff2');unicode-range:U+0600-06FF,U+0750-077F,U+0870-088E,U+0890-0891,U+0897-08E1,U+08E3-08FF,U+200C-200E,U+2010-2011,U+204F,U+2E41,U+FB50-FDFF,U+FE70-FE74,U+FE76-FEFC}
@font-face{font-family:Vazirmatn;font-weight:700;src:url(${fonts}/vazirmatn-arabic-700.woff2) format('woff2');unicode-range:U+0600-06FF,U+0750-077F,U+0870-088E,U+0890-0891,U+0897-08E1,U+08E3-08FF,U+200C-200E,U+2010-2011,U+204F,U+2E41,U+FB50-FDFF,U+FE70-FE74,U+FE76-FEFC}
@font-face{font-family:Inter;font-weight:400;src:url(${fonts}/inter-latin-400.woff2) format('woff2')}
@font-face{font-family:Inter;font-weight:600;src:url(${fonts}/inter-latin-600.woff2) format('woff2')}
@font-face{font-family:Inter;font-weight:700;src:url(${fonts}/inter-latin-700.woff2) format('woff2')}
:root{--bg:#0e1621;--bar:#17212b;--in:#182533;--out:#2b5278;--text:#f5f6f7;--muted:#7f91a4;--link:#6ab3f3;
 --key:#243447;--btn:#26384c;--success:#2f9e5c;--primary:#2f7fd1;--danger:#d24b45;}
*{box-sizing:border-box;margin:0}
html,body{background:transparent}
body{width:390px;font-family:Vazirmatn,Inter,system-ui,sans-serif;color:var(--text);-webkit-font-smoothing:antialiased}
.phone{position:relative;width:390px;background:var(--bg);overflow:hidden;display:flex;flex-direction:column;border-radius:34px;
 background-image:radial-gradient(circle at 20% 10%,rgba(20,184,166,.07),transparent 40%),radial-gradient(circle at 85% 80%,rgba(43,82,120,.18),transparent 45%)}
.status{height:34px;flex:none;display:flex;justify-content:space-between;align-items:flex-end;padding:0 26px 4px;font:600 14px Inter,sans-serif;background:var(--bar);direction:ltr}
.status i{display:inline-block;width:24px;height:11px;border:1.5px solid #cbd5e1;border-radius:3px;position:relative}
.status i:after{content:"";position:absolute;inset:1.5px 5px 1.5px 1.5px;background:#cbd5e1;border-radius:1px}
.head{height:54px;flex:none;display:flex;align-items:center;gap:12px;padding:0 14px;background:var(--bar);border-bottom:1px solid #0b121a;direction:ltr}
.head .back{color:var(--link);font:400 26px Inter;line-height:1;width:14px}
.head img{width:38px;height:38px;border-radius:50%}
.head .who{display:flex;flex-direction:column;line-height:1.2;direction:ltr}
.head b{font:600 15px Inter,sans-serif}
.head small{font:400 12px Inter,sans-serif;color:var(--muted)}
.chat{flex:1;display:flex;flex-direction:column;justify-content:flex-end;gap:5px;padding:10px 10px 8px;overflow:hidden}
.msg{max-width:84%;display:flex;flex-direction:column;gap:3px}
.msg.in{align-self:flex-start}.msg.out{align-self:flex-end}
.bubble{padding:7px 11px 6px;border-radius:15px;font-size:14.5px;line-height:1.5;background:var(--in)}
.msg.out .bubble{background:var(--out)}
.bubble b{font-weight:700}
.bubble code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:13px;color:#8fd0ff;background:rgba(255,255,255,.06);padding:0 3px;border-radius:4px;word-break:break-all;direction:ltr;unicode-bidi:isolate}
.time{display:block;text-align:end;font:400 11px Inter,sans-serif;color:var(--muted);margin-top:1px;direction:ltr}
.msg.out .time{color:#9fc0e6}
.rtl{direction:rtl;text-align:right}.ltr{direction:ltr;text-align:left}
.kb{display:flex;flex-direction:column;gap:3px}
.kb .row{display:flex;gap:3px}
.kb.rtl .row{flex-direction:row-reverse}
.btn{flex:1;min-height:38px;display:flex;align-items:center;justify-content:center;text-align:center;padding:6px 8px;border-radius:9px;background:var(--btn);font-size:13.5px;font-weight:600;line-height:1.3;color:#e9eef4}
.btn.success{background:var(--success);color:#fff}.btn.primary{background:var(--primary);color:#fff}.btn.danger{background:var(--danger);color:#fff}
.photo{width:200px;height:200px;border-radius:12px;background:#fff;display:flex;align-items:center;justify-content:center;overflow:hidden}
.photo svg{display:block}
.reply{flex:none;background:var(--bar);padding:7px 8px 8px;display:flex;flex-direction:column;gap:6px;border-top:1px solid #0b121a}
.reply .row{display:flex;gap:6px}
.reply.rtl .row{flex-direction:row-reverse}
.key{flex:1;min-height:40px;display:flex;align-items:center;justify-content:center;text-align:center;border-radius:8px;background:var(--key);font-size:14px;font-weight:600;padding:5px 6px;color:#eef3f8;box-shadow:0 1px 0 rgba(0,0,0,.35)}
.key.success{background:var(--success);color:#fff}.key.primary{background:var(--primary);color:#fff}.key.danger{background:var(--danger);color:#fff}
.input{flex:none;height:48px;background:var(--bar);display:flex;align-items:center;gap:10px;padding:0 12px;border-top:1px solid #0b121a}
.input .field{flex:1;height:34px;border-radius:17px;background:#0e1621;color:var(--muted);display:flex;align-items:center;padding:0 14px;font-size:14px}
.input .icon{width:22px;height:22px;border-radius:50%;border:2px solid var(--muted);opacity:.7}
.home{height:20px;flex:none;background:var(--bar);display:flex;justify-content:center;align-items:flex-start}
.home:after{content:"";width:120px;height:4px;border-radius:2px;background:#e5e7eb;opacity:.85;margin-top:6px}
`

function qrSvg() {
  // A QR-code-like placeholder: three finder squares and pseudo-random modules. It does not encode anything.
  const n = 25, s = 8
  let seed = 7
  const rnd = () => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff)
  let cells = ''
  const finder = (x, y) => `<rect x="${x * s}" y="${y * s}" width="${7 * s}" height="${7 * s}" fill="#0f172a"/><rect x="${(x + 1) * s}" y="${(y + 1) * s}" width="${5 * s}" height="${5 * s}" fill="#fff"/><rect x="${(x + 2) * s}" y="${(y + 2) * s}" width="${3 * s}" height="${3 * s}" fill="#0f172a"/>`
  for (let y = 0; y < n; y++)
    for (let x = 0; x < n; x++) {
      const inFinder = (x < 8 && y < 8) || (x > n - 9 && y < 8) || (x < 8 && y > n - 9)
      if (!inFinder && rnd() > 0.52) cells += `<rect x="${x * s}" y="${y * s}" width="${s}" height="${s}" fill="#0f172a"/>`
    }
  return `<svg width="${n * s}" height="${n * s}" viewBox="0 0 ${n * s} ${n * s}" xmlns="http://www.w3.org/2000/svg">${cells}${finder(0, 0)}${finder(n - 7, 0)}${finder(0, n - 7)}</svg>`
}
function receiptSvg() {
  return `<svg width="200" height="200" viewBox="0 0 200 200" xmlns="http://www.w3.org/2000/svg"><rect width="200" height="200" fill="#f4f6f8"/><rect x="34" y="14" width="132" height="172" rx="8" fill="#fff" stroke="#d7dde4"/><circle cx="100" cy="46" r="14" fill="#14b8a6"/><path d="M93 46l5 5 9-10" stroke="#fff" stroke-width="3.5" fill="none" stroke-linecap="round" stroke-linejoin="round"/><g fill="#e2e8f0"><rect x="48" y="80" width="50" height="6" rx="3"/><rect x="48" y="102" width="40" height="6" rx="3"/><rect x="48" y="124" width="56" height="6" rx="3"/><rect x="48" y="146" width="44" height="6" rx="3"/></g><g fill="#94a3b8"><rect x="112" y="80" width="40" height="6" rx="3"/><rect x="120" y="102" width="32" height="6" rx="3"/><rect x="106" y="124" width="46" height="6" rx="3"/><rect x="116" y="146" width="36" height="6" rx="3"/></g></svg>`
}

const kbHtml = (rows, lang) =>
  `<div class="kb ${fa(lang) ? 'rtl' : ''}">${rows.map((r) => `<div class="row">${r.map((b) => `<div class="btn ${b.style}">${esc(b.text)}</div>`).join('')}</div>`).join('')}</div>`

function msgHtml(m, lang) {
  const dir = m.dir || (fa(lang) ? 'rtl' : 'ltr')
  const photo = m.photo === 'qr' ? `<div class="photo">${qrSvg()}</div>` : m.photo === 'receipt' ? `<div class="photo">${receiptSvg()}</div>` : ''
  const body = m.text ? `<div class="${dir}">${html(m.text)}</div>` : ''
  return `<div class="msg ${m.from}"><div class="bubble">${photo}${photo && body ? '<div style="height:6px"></div>' : ''}${body}<span class="time">${clock(lang, m.time || '10:42')}</span></div>${m.kb ? kbHtml(m.kb, m.kbLang || lang) : ''}</div>`
}

function page({ lang, messages, reply, placeholder, height = 844 }) {
  const replyHtml = reply
    ? `<div class="reply ${fa(lang) ? 'rtl' : ''}">${reply.map((r) => `<div class="row">${r.map((b) => `<div class="key ${b.style}">${esc(b.text)}</div>`).join('')}</div>`).join('')}</div>`
    : ''
  const field = reply ? placeholder : fa(lang) ? 'پیام' : 'Message'
  return `<!doctype html><html lang="${lang}"><meta charset="utf-8"><style>${css}</style><body><div class="phone" style="height:${height}px">
<div class="status"><span>9:41</span><i></i></div>
<div class="head"><span class="back">‹</span><img src="${avatar}" alt=""><div class="who"><b>${BRAND}</b><small>bot</small></div></div>
<div class="chat">${messages.map((m) => msgHtml(m, lang)).join('')}</div>
${replyHtml}
<div class="input"><div class="field ${fa(lang) ? 'rtl' : ''}">${esc(field)}</div><div class="icon"></div></div>
<div class="home"></div></div></body></html>`
}

// ---- the flows -------------------------------------------------------------------------------------
function menuKeyboard(lang) {
  // the bottom menu of a customer, as mainMenu builds it (internal/bot/handler/menu.go)
  return [[B(lang, 'btn.buy')], [B(lang, 'btn.services'), B(lang, 'btn.wallet')], [B(lang, 'btn.trial'), B(lang, 'btn.support')], [B(lang, 'btn.invite'), B(lang, 'btn.language')]]
}
function homeInline(lang) {
  // the inline main menu (showHome)
  return [[B(lang, 'btn.buy'), B(lang, 'btn.services')], [B(lang, 'btn.wallet'), B(lang, 'btn.trial')], [B(lang, 'btn.support'), B(lang, 'btn.language')], [B(lang, 'btn.invite')]]
}
const planRows = (lang) => PLANS[lang].map(([name, price]) => [{ text: T(lang, 'plan.button', { name, price: money(lang, price) }), style: '' }])
const limits = (lang, days, gb) =>
  T(lang, 'plan.days', { n: digits(lang, days) }) + '\n' + (gb ? T(lang, 'plan.traffic', { size: bytes(lang, gb) }) : T(lang, 'plan.unlimited_traffic'))

const tap = (lang, key, time) => ({ from: 'out', text: T(lang, key), time })

const flows = {
  // 1. /start: the language picker (the same for every customer)
  start: (lang) => ({
    lang: 'en',
    height: 470,
    messages: [
      { from: 'out', text: '/start', dir: 'ltr', time: '10:40' },
      { from: 'in', text: T('en', 'lang.prompt'), dir: 'ltr', kb: [[{ text: 'فارسی 🇮🇷', style: '' }, { text: 'English 🇬🇧', style: '' }]], kbLang: 'en', time: '10:40' },
    ],
  }),
  // 2. the welcome, with the colored bottom menu
  welcome: (lang) => ({
    lang,
    messages: [
      { from: 'in', text: T(lang, 'lang.changed'), time: '10:40' },
      { from: 'in', text: T(lang, 'welcome', { brand: BRAND }), time: '10:40' },
      { from: 'in', text: T(lang, 'menu.title', { brand: BRAND }), kb: homeInline(lang), time: '10:40' },
    ],
    reply: menuKeyboard(lang),
    placeholder: T(lang, 'menu.placeholder'),
  }),
  // 3. buying: the plan list, then a plan's payment methods
  plans: (lang) => ({
    lang,
    messages: [
      tap(lang, 'btn.buy', '10:41'),
      { from: 'in', text: T(lang, 'plans.title'), kb: [...planRows(lang), [B(lang, 'btn.home')]], time: '10:41' },
    ],
    reply: menuKeyboard(lang),
    placeholder: T(lang, 'menu.placeholder'),
  }),
  checkout: (lang) => {
    const [name, price, days, gb] = PLANS[lang][1]
    return {
      lang,
      messages: [
        tap(lang, 'btn.buy', '10:41'),
        {
          from: 'in',
          text: T(lang, 'plan.detail', { name, limits: limits(lang, days, gb), price: money(lang, price) }),
          // The methods the demo store has switched on, as the dashboard pictures show them: automatic USDT is off
          // there (it is optional and needs a subscription), so the manual USDT button is the one drawn here.
          kb: [
            [B(lang, 'btn.pay_wallet', { balance: money(lang, 150000) })],
            [B(lang, 'btn.pay_card'), B(lang, 'btn.pay_crypto')],
            [B(lang, 'btn.pay_stars')],
            [B(lang, 'btn.discount')],
            [B(lang, 'btn.back'), B(lang, 'btn.home')],
          ],
          time: '10:41',
        },
      ],
      reply: menuKeyboard(lang),
      placeholder: T(lang, 'menu.placeholder'),
    }
  },
  // 4. card to card: the instructions, the receipt, the answer
  card: (lang) => ({
    lang,
    messages: [
      {
        from: 'in',
        text: T(lang, 'pay.card', { amount: money(lang, PLANS[lang][1][1]), card: '0000 0000 0000 0000', holder: lang === 'fa' ? 'فروشگاه نمونه' : 'Demo Store', ref: 'A7K2-Q9' }),
        kb: [[B(lang, 'btn.cancel')]],
        time: '10:42',
      },
      { from: 'out', photo: 'receipt', text: lang === 'fa' ? '۸۱۴۴۲۰۹۱۷۲۳۶' : '814420917236', dir: 'ltr', time: '10:44' },
      { from: 'in', text: T(lang, 'pay.submitted'), time: '10:44' },
    ],
  }),
  // 5. delivery: approved, the subscription link, the QR code
  delivered: (lang) => ({
    lang,
    messages: [
      { from: 'in', text: T(lang, 'pay.approved'), time: '10:51' },
      {
        from: 'in',
        text: T(lang, 'sub.ready', { link: LINK, expires: date(lang, plusDays(30)), traffic: bytes(lang, 30) }),
        kb: [[B(lang, 'btn.services')]],
        time: '10:51',
      },
      { from: 'in', photo: 'qr', text: T(lang, 'sub.qr_caption') + '\n<code>' + LINK + '</code>', time: '10:51' },
    ],
  }),
  // 6. a service and its renewal
  renew: (lang) => ({
    lang,
    messages: [
      tap(lang, 'btn.services', '10:50'),
      {
        from: 'in',
        text:
          T(lang, 'sub.detail', { id: 'a3f9c2', status: T(lang, 'sub.status.active'), expires: date(lang, plusDays(9)), traffic: bytes(lang, 30), link: LINK }) +
          '\n' + T(lang, 'sub.usage', { used: bytes(lang, 24), total: bytes(lang, 30), bar: '▰▰▰▰▰▰▰▰▱▱' }),
        kb: [[B(lang, 'btn.renew'), B(lang, 'btn.add_traffic')], [B(lang, 'btn.qr')], [B(lang, 'btn.back'), B(lang, 'btn.home')]],
        time: '10:50',
      },
      { from: 'in', text: T(lang, 'sub.renewed', { id: 'a3f9c2', expires: date(lang, plusDays(39)), traffic: bytes(lang, 60) }), time: '10:52' },
    ],
  }),
  // 7. the wallet
  wallet: (lang) => ({
    lang,
    messages: [
      tap(lang, 'btn.wallet', '10:46'),
      { from: 'in', text: T(lang, 'wallet.title', { balance: money(lang, 150000) }), kb: [[B(lang, 'btn.topup'), B(lang, 'btn.history')], [B(lang, 'btn.home')]], time: '10:46' },
      { from: 'in', text: T(lang, 'wallet.credited', { amount: money(lang, 200000), balance: money(lang, 350000) }), time: '10:48' },
    ],
    reply: menuKeyboard(lang),
    placeholder: T(lang, 'menu.placeholder'),
  }),
}

const browser = await puppeteer.launch({ executablePath: CHROME, headless: true, args: ['--no-first-run', '--allow-file-access-from-files'] })
const pg = await browser.newPage()
await pg.setViewport({ width: 390, height: 844, deviceScaleFactor: 2 })
for (const [name, build] of Object.entries(flows)) {
  for (const lang of name === 'start' ? ['en'] : ['fa', 'en']) {
    const spec = build(lang)
    const file = path.join(out, `${name}-${name === 'start' ? 'both' : lang}`)
    fs.writeFileSync(file + '.html', page({ ...spec, lang: spec.lang }))
    await pg.goto(pathToFileURL(file + '.html').href, { waitUntil: 'networkidle0' })
    await pg.evaluate(() => document.fonts.ready)
    await pg.screenshot({ path: file + '.png', omitBackground: true, clip: { x: 0, y: 0, width: 390, height: spec.height || 844 } })
    fs.rmSync(file + '.html')
    console.log('wrote', path.basename(file) + '.png')
  }
}
await browser.close()
