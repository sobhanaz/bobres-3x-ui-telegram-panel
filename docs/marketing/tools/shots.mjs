// Dashboard screenshots of the lab (seeded with seed-demo.py), in Persian (RTL) and English (LTR).
// Run from docs/marketing/tools: node receipt.mjs && node shots.mjs && node convert-shots.mjs
import fs from 'node:fs'
import { launch, login, BASE, sleep } from './lib.mjs'

const OUT = process.env.OUT || 'out/raw'
fs.mkdirSync(OUT, { recursive: true })
const receipt = fs.readFileSync('out/receipt.png')
const avatar = new URL('../../brand/bot-avatar.jpg', import.meta.url).pathname

const b = await launch()
const page = await b.newPage()
await page.emulateMediaFeatures([{ name: 'prefers-reduced-motion', value: 'reduce' }])

// The demo receipts are pictures made for the demo: Telegram holds no files for the demo customers.
await page.setRequestInterception(true)
page.on('request', (req) => {
  if (/\/api\/v1\/payments\/[^/]+\/receipt/.test(req.url())) return req.respond({ status: 200, contentType: 'image/png', body: receipt })
  req.continue()
})
// The license state will follow the pricing policy, and a screenshot should not date: no license chip.
const css = '.license-chip{display:none!important}'

async function settle(ms = 1400) {
  await sleep(ms)
  await page.addStyleTag({ content: css })
  await sleep(150)
}
async function goto(path, ms) {
  await page.goto(BASE + path, { waitUntil: 'networkidle2' })
  await settle(ms)
}
async function setLang(lang) {
  await page.evaluate((l) => localStorage.setItem('bobres.lang', l), lang)
  await page.reload({ waitUntil: 'networkidle2' })
  await settle()
}
async function clickTab(re) {
  for (const t of await page.$$('[role=tab]')) {
    if (re.test(await t.evaluate((e) => e.textContent.trim()))) { await t.click(); await sleep(1500); return true }
  }
  return false
}
async function shot(name, opts = {}) {
  await page.screenshot({ path: `${OUT}/${name}.png`, ...opts })
}
async function elementShot(name, handle, pad = 0) {
  const box = await handle.boundingBox()
  await page.screenshot({ path: `${OUT}/${name}.png`, clip: { x: Math.max(0, box.x - pad), y: Math.max(0, box.y - pad), width: box.width + 2 * pad, height: box.height + 2 * pad } })
}

await page.setViewport({ width: 1440, height: 900 })
await login(page, 'en')
await settle()

for (const lang of ['en', 'fa']) {
  await page.setViewport({ width: 1440, height: 900 })
  await page.goto(BASE + '/admin/', { waitUntil: 'networkidle2' })
  await setLang(lang)

  await goto('/admin/'); await shot(`${lang}-overview`)
  await goto('/admin/plans'); await shot(`${lang}-plans`)
  await goto('/admin/payments'); await shot(`${lang}-payments`)
  await goto('/admin/support'); await shot(`${lang}-support`)

  // Branding: the store tab, with a bot photo picked (not uploaded) to see the card in use.
  await goto('/admin/branding')
  const input = await page.$('input[type=file][accept*="png" i], input[type=file]')
  const inputs = await page.$$('input[type=file]')
  // the second file input is the bot photo's (the first is the logo's)
  const botInput = inputs.length > 1 ? inputs[1] : input
  await botInput.uploadFile(avatar)
  await sleep(1200)
  await page.evaluate(() => document.activeElement && document.activeElement.blur())
  await page.evaluate(() => window.scrollTo(0, 190))
  await sleep(300)
  await shot(`${lang}-branding`)
  await page.evaluate(() => window.scrollTo(0, 0))

  // System -> HTTPS: the card where the mode is chosen. System -> Backups: the top of the tab.
  await goto('/admin/system')
  let tabs = await page.$$('[role=tab]')
  await tabs[3].click(); await sleep(1500)
  const card = await page.evaluateHandle(() => document.querySelector('.p-radiobutton-input').closest('.p-card'))
  await elementShot(`${lang}-https`, card.asElement(), 0)
  tabs = await page.$$('[role=tab]')
  await tabs[2].click(); await sleep(1500)
  const panel = await page.$('.p-tabpanel-active')
  const pb = await panel.boundingBox()
  await page.screenshot({ path: `${OUT}/${lang}-backups.png`, clip: { x: pb.x, y: pb.y - 14, width: pb.width, height: 452 } })
}

// A few on a phone.
for (const lang of ['en', 'fa']) {
  await page.setViewport({ width: 390, height: 844, deviceScaleFactor: 2, isMobile: true, hasTouch: true })
  await setLang(lang)
  await goto('/admin/'); await shot(`${lang}-overview-m`)
  await goto('/admin/payments'); await shot(`${lang}-payments-m`)
  await goto('/admin/plans'); await shot(`${lang}-plans-m`)
}
await b.close()
