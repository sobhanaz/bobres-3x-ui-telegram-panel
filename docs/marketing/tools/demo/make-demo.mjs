// The short demo, per language: slides (HTML, drawn with puppeteer-core) from the dashboard screenshots and the
// bot illustrations in site/assets, joined with ffmpeg crossfades. No audio, H.264.
//
//   node make-demo.mjs        writes site/assets/demo-{fa,en}.mp4 and demo-{fa,en}-poster.webp
//
// Needs Chrome, ffmpeg and sharp. Run the screenshots and the chat renderer first (../README.md).
import fs from 'node:fs'
import path from 'node:path'
import { execFileSync } from 'node:child_process'
import { fileURLToPath, pathToFileURL } from 'node:url'
import puppeteer from 'puppeteer-core'
import sharp from 'sharp'

const here = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(here, '../../../..')
const assets = path.join(repo, 'site/assets')
const work = path.join(here, 'out')
fs.mkdirSync(work, { recursive: true })
const CHROME = process.env.CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
const url = (p) => pathToFileURL(path.join(assets, p)).href

const T = {
  fa: {
    dir: 'rtl',
    tagline: 'فروشگاه VPN شما، روی تلگرام',
    sub: 'هستهٔ رایگان · ۳۰ روز آزمایش امکانات ویژه',
    c: [
      'مشتری زبان را انتخاب می‌کند و منوی رنگی را می‌بیند',
      'پلن را انتخاب کنید، روش پرداخت را بزنید',
      'کارت به کارت پرداخت کنید و رسید را بفرستید',
      'رسیدها را در داشبورد تأیید می‌کنید',
      'سرویس با لینک اشتراک و کد QR تحویل می‌شود',
      'فروش، سرویس‌ها و سلامت سرورها در یک نگاه',
    ],
    install: 'با یک دستور روی سرور خودتان نصب کنید',
    steps: ['نصب', 'ساخت پلن', 'فروش'],
    outro: 'نصب در یک دستور، روی سرور خودتان',
    note: 'تصویرها نمونه‌اند و با داده‌های آزمایشی ساخته شده‌اند',
  },
  en: {
    dir: 'ltr',
    tagline: 'Your VPN store on Telegram',
    sub: 'Free core · 30-day trial of the premium features',
    c: [
      'Customers pick a language and get a colored menu',
      'Pick a plan, choose how to pay',
      'Pay card to card and send the receipt',
      'You approve receipts in the dashboard',
      'The service arrives with its link and a QR code',
      'Sales, services and server health at a glance',
    ],
    install: 'Install it on your own server with one command',
    steps: ['Install', 'Add plans', 'Sell'],
    outro: 'One-command install, on your own server',
    note: 'Screens are samples, made with demo data',
  },
}

const css = `
@font-face{font-family:Vazirmatn;font-weight:400;src:url(${url('fonts/vazirmatn-arabic-400.woff2')});unicode-range:U+0600-06FF,U+200C-200E,U+2010-2011,U+FB50-FDFF,U+FE70-FEFC}
@font-face{font-family:Vazirmatn;font-weight:700;src:url(${url('fonts/vazirmatn-arabic-700.woff2')});unicode-range:U+0600-06FF,U+200C-200E,U+2010-2011,U+FB50-FDFF,U+FE70-FEFC}
@font-face{font-family:Inter;font-weight:400;src:url(${url('fonts/inter-latin-400.woff2')})}
@font-face{font-family:Inter;font-weight:700;src:url(${url('fonts/inter-latin-700.woff2')})}
*{box-sizing:border-box;margin:0}
body{width:1280px;height:720px;overflow:hidden;font-family:Vazirmatn,Inter,system-ui,sans-serif;color:#e7edf6;
 background:radial-gradient(900px 500px at 85% 0%,rgba(20,184,166,.20),transparent 70%),radial-gradient(700px 420px at 0% 100%,rgba(139,90,43,.18),transparent 70%),#0f172a}
.slide{position:relative;width:1280px;height:720px;display:flex;align-items:center;justify-content:center;padding:48px 72px;gap:56px}
.mark{position:absolute;inset-block-end:26px;inset-inline-start:34px;display:flex;align-items:center;gap:10px;font:700 20px Inter,sans-serif;letter-spacing:.06em;color:#a3b3c9}
.mark img{width:30px;height:30px}
.note{position:absolute;inset-block-end:30px;inset-inline-end:34px;font-size:17px;color:#7f91a4}
.cap{font-size:50px;font-weight:700;line-height:1.5;max-width:560px;text-wrap:balance}
.pill{display:inline-block;margin-bottom:22px;padding:4px 18px;border:1px solid #243456;border-radius:999px;background:#15213b;color:#f5deb3;font:700 20px Inter,Vazirmatn,sans-serif;direction:ltr}
.phone img{height:640px;display:block;filter:drop-shadow(0 24px 40px rgba(2,6,23,.6))}
.shot{flex-direction:column;gap:26px}
.shot .cap{max-width:1000px;text-align:center;font-size:44px}
.shot>img{width:880px;height:550px;object-fit:cover;object-position:top;border-radius:16px;border:1px solid #243456;box-shadow:0 24px 60px rgba(2,6,23,.6)}
.title .mascot{width:420px;height:420px;border-radius:50%;object-fit:cover;-webkit-mask-image:radial-gradient(closest-side,#000 84%,transparent 100%)}
.title h1{font:700 104px Inter,sans-serif;letter-spacing:.05em;line-height:1}
.title .tag{font-size:46px;font-weight:700;margin-top:22px;line-height:1.4}
.title .sub{margin-top:18px;font-size:26px;color:#a3b3c9}
.term{width:880px;background:#0b1325;border:1px solid #243456;border-radius:18px;padding:30px 34px;box-shadow:0 24px 60px rgba(2,6,23,.6);direction:ltr}
.term .dots{display:flex;gap:9px;margin-bottom:22px}.term .dots i{width:14px;height:14px;border-radius:50%;background:#243456}
.term code{display:block;font:400 25px/1.55 ui-monospace,Menlo,monospace;color:#e7edf6;overflow-wrap:anywhere}
.term code b{color:#5eead4;font-weight:400}
.steps{display:flex;gap:18px;margin-top:6px}
.steps span{display:flex;align-items:center;gap:12px;padding:12px 26px;background:#15213b;border:1px solid #243456;border-radius:999px;font-size:30px;font-weight:700}
.steps em{font:700 22px Inter,sans-serif;font-style:normal;display:grid;place-items:center;width:38px;height:38px;border-radius:50%;background:#14b8a6;color:#04211e}
.stack{display:flex;flex-direction:column;align-items:center;gap:30px}
.outro .mascot{width:300px;height:300px;border-radius:50%;object-fit:cover;-webkit-mask-image:radial-gradient(closest-side,#000 84%,transparent 100%)}
.repo{font:400 25px Inter,sans-serif;color:#5eead4;direction:ltr}
`

const wrap = (lang, body) => `<!doctype html><html lang="${lang}" dir="${T[lang].dir}"><meta charset="utf-8"><style>${css}</style><body>${body}</body></html>`
const mark = `<div class="mark"><img src="${url('brand/bobres-icon.svg')}" alt="">BOBRES</div>`

function slides(lang) {
  const t = T[lang]
  const phone = (img, i) => `<div class="slide">${mark}<div><div class="pill">${lang === 'fa' ? String(i).replace(/\d/g, (d) => '۰۱۲۳۴۵۶۷۸۹'[d]) + ' / ۶' : i + ' / 6'}</div><div class="cap">${t.c[i - 1]}</div></div><div class="phone"><img src="${url(`chat/chat-${img}-${lang}.webp`)}" alt=""></div></div>`
  const shot = (img, i) => `<div class="slide shot">${mark}<div class="cap">${t.c[i - 1]}</div><img src="${url(`shots/dash-${lang}-${img}.webp`)}" alt=""></div>`
  return [
    `<div class="slide title">${mark}<img class="mascot" src="${url('brand/bobres-mascot.webp')}" alt=""><div><h1>BOBRES</h1><div class="tag">${t.tagline}</div><div class="sub">${t.sub}</div></div></div>`,
    phone('welcome', 1),
    phone('checkout', 2),
    phone('card', 3),
    shot('payments', 4),
    phone('delivered', 5),
    shot('overview', 6),
    `<div class="slide stack">${mark}<div class="cap" style="max-width:1000px;text-align:center;font-size:44px">${t.install}</div><div class="term"><div class="dots"><i></i><i></i><i></i></div><code><b>$</b> bash &lt;(curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh)</code></div><div class="steps">${t.steps.map((s, i) => `<span><em>${lang === 'fa' ? '۱۲۳'[i] : i + 1}</em>${s}</span>`).join('')}</div></div>`,
    `<div class="slide outro stack"><img class="mascot" src="${url('brand/bobres-mascot.webp')}" alt=""><div class="cap" style="max-width:1000px;text-align:center;font-size:48px">${t.outro}</div><div class="sub" style="font-size:26px;color:#a3b3c9">${t.sub}</div><div class="repo">github.com/sobhanaz/bobres-3x-ui-telegram-panel</div><div class="note">${t.note}</div></div>`,
  ]
}

const SHOW = 2.7 // seconds each slide is shown, a crossfade of 0.5 s on top
const FADE = 0.5
const browser = await puppeteer.launch({ executablePath: CHROME, headless: true, args: ['--allow-file-access-from-files', '--no-first-run'] })
const page = await browser.newPage()
await page.setViewport({ width: 1280, height: 720, deviceScaleFactor: 1 })
for (const lang of ['fa', 'en']) {
  const files = []
  const list = slides(lang)
  for (let i = 0; i < list.length; i++) {
    const f = path.join(work, `${lang}-${i}.html`)
    fs.writeFileSync(f, wrap(lang, list[i]))
    await page.goto(pathToFileURL(f).href, { waitUntil: 'networkidle0' })
    await page.evaluate(() => document.fonts.ready)
    const png = path.join(work, `${lang}-${i}.png`)
    await page.screenshot({ path: png })
    files.push(png)
  }
  // crossfades: every input lasts SHOW + FADE, the k-th fade starts at SHOW * k
  const inputs = files.flatMap((f) => ['-loop', '1', '-t', String(SHOW + FADE), '-framerate', '25', '-i', f])
  let graph = ''
  let last = '[0:v]'
  for (let k = 1; k < files.length; k++) {
    const out = k === files.length - 1 ? '[v]' : `[x${k}]`
    graph += `${last}[${k}:v]xfade=transition=fade:duration=${FADE}:offset=${(SHOW * k).toFixed(2)}${out};`
    last = out
  }
  const mp4 = path.join(assets, `demo-${lang}.mp4`)
  execFileSync('ffmpeg', ['-y', '-v', 'error', ...inputs, '-filter_complex', graph.slice(0, -1), '-map', '[v]', '-c:v', 'libx264', '-preset', 'veryslow', '-crf', '23',
    '-tune', 'stillimage', '-pix_fmt', 'yuv420p', '-r', '25', '-movflags', '+faststart', '-an', mp4], { stdio: 'inherit' })
  await sharp(files[0]).webp({ quality: 82, effort: 6 }).toFile(path.join(assets, `demo-${lang}-poster.webp`))
  console.log(lang, 'done', (fs.statSync(mp4).size / 1024).toFixed(0) + ' KB')
}
await browser.close()
