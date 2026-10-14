// JPG images for the launch posts (docs/marketing/images/{fa,en}/): Telegram turns a dropped WebP into a sticker, so
// the posts use JPG. They are made from the site's pictures, the demo's frames and a screenshot of the tiers table.
// The launch picture (01-launch.jpg) comes from make-brand-images.mjs.
//
//   node make-post-images.mjs     (run the screenshots, the chat renderer and the demo first, see ../README.md)
import fs from 'node:fs'
import http from 'node:http'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import puppeteer from 'puppeteer-core'
import sharp from 'sharp'

const here = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(here, '../../..')
const site = path.join(repo, 'site')
const out = path.join(repo, 'docs/marketing/images')
const CHROME = process.env.CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
const NAVY = '#0f172a'

// a tiny static server, so the page is shown as it is served
const types = { '.html': 'text/html; charset=utf-8', '.css': 'text/css', '.js': 'text/javascript', '.webp': 'image/webp', '.woff2': 'font/woff2', '.svg': 'image/svg+xml', '.jpg': 'image/jpeg', '.png': 'image/png' }
const server = http.createServer((req, res) => {
  let p = decodeURIComponent(new URL(req.url, 'http://x').pathname)
  if (p.endsWith('/')) p += 'index.html'
  const f = path.join(site, p)
  if (!f.startsWith(site) || !fs.existsSync(f)) { res.writeHead(404); return res.end() }
  res.writeHead(200, { 'content-type': types[path.extname(f)] || 'application/octet-stream' })
  fs.createReadStream(f).pipe(res)
}).listen(0)
const base = `http://127.0.0.1:${server.address().port}`

const jpg = (img, file) => img.flatten({ background: NAVY }).jpeg({ quality: 88, mozjpeg: true }).toFile(file)
const demoOut = path.join(here, 'demo/out')

const browser = await puppeteer.launch({ executablePath: CHROME, headless: true, args: ['--no-first-run'] })
for (const lang of ['fa', 'en']) {
  const dir = path.join(out, lang)
  fs.mkdirSync(dir, { recursive: true })
  const asset = (p) => path.join(site, 'assets', p)
  await jpg(sharp(asset(`shots/dash-${lang}-overview.webp`)), path.join(dir, '02-why-overview.jpg'))
  await jpg(sharp(asset(`chat/chat-welcome-${lang}.webp`)), path.join(dir, '03-features-a-welcome.jpg'))
  await jpg(sharp(asset(`chat/chat-checkout-${lang}.webp`)), path.join(dir, '03-features-b-checkout.jpg'))
  await jpg(sharp(asset(`shots/dash-${lang}-payments.webp`)), path.join(dir, '03-features-c-payments.jpg'))
  await jpg(sharp(path.join(demoOut, `${lang}-7.png`)), path.join(dir, '04-install.jpg'))
  await jpg(sharp(asset(`shots/dash-${lang}-https.webp`)), path.join(dir, '05-safety-a-https.jpg'))
  await jpg(sharp(asset(`shots/dash-${lang}-backups.webp`)), path.join(dir, '05-safety-b-backups.jpg'))
  await jpg(sharp(asset(`shots/dash-${lang}-support.webp`)), path.join(dir, '06-faq-support.jpg'))
  await jpg(sharp(asset(`demo-${lang}-poster.webp`)), path.join(dir, '08-cta.jpg'))

  // the tiers table, as the page shows it (dark); the page's sticky header would cover the table's top: it is switched off
  const page = await browser.newPage()
  await page.emulateMediaFeatures([{ name: 'prefers-color-scheme', value: 'dark' }])
  await page.setViewport({ width: 1100, height: 900, deviceScaleFactor: 1.5 })
  await page.goto(`${base}${lang === 'fa' ? '/' : '/en/'}`, { waitUntil: 'networkidle0' })
  await page.addStyleTag({ content: '.top{position:static!important}' })
  const table = await page.$('.table-wrap')
  await table.screenshot({ path: path.join(dir, '07-free-vs-paid.png') })
  await page.close()
  await jpg(sharp(path.join(dir, '07-free-vs-paid.png')), path.join(dir, '07-free-vs-paid.jpg'))
  fs.rmSync(path.join(dir, '07-free-vs-paid.png'))
  console.log(lang, fs.readdirSync(dir).length, 'images')
}
await browser.close()
server.close()
