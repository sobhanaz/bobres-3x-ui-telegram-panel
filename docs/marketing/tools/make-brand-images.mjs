// The banner-based pictures, made from docs/brand/bobres-banner.webp (the mascot, the name and an English tagline):
//
//   site/assets/brand/og-image.jpg   the link-preview picture for both pages: the mascot and the name, no tagline, so it
//                                    is right in Persian and in English (1200 x 630)
//   images/en/01-launch.jpg          the banner with its English tagline
//   images/fa/01-launch.jpg          the same banner with a Persian tagline instead (drawn with Chrome and Vazirmatn)
//
//   node make-brand-images.mjs       (needs Chrome and sharp, see ../README.md)
//
// The tagline is taken out by filling the pixels under it from the rows above and below, column by column: the
// background there is a smooth dark gradient, so nothing shows. The output carries no metadata.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import puppeteer from 'puppeteer-core'
import sharp from 'sharp'

const here = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(here, '../../..')
const banner = path.join(repo, 'docs/brand/bobres-banner.webp')
const CHROME = process.env.CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
const W = 1200
const H = 630
const work = path.join(here, 'out')
fs.mkdirSync(work, { recursive: true })

// the banner without its tagline, as a PNG file (full size, 1376 x 768)
async function withoutTagline() {
  const { data, info } = await sharp(banner).removeAlpha().raw().toBuffer({ resolveWithObject: true })
  const { width: w, height: h, channels: c } = info
  const x0 = 686, x1 = 1296 // the tagline sits between these columns ...
  const y0 = 432, y1 = 490 // ... and these rows (BOBRES, with its glow, ends above y0; the circuit lines stay outside)
  for (let x = x0; x <= x1; x++) {
    for (let ch = 0; ch < c; ch++) {
      const top = data[((y0 - 1) * w + x) * c + ch]
      const bottom = data[((y1 + 1) * w + x) * c + ch]
      for (let y = y0; y <= y1; y++) {
        const t = (y - y0 + 1) / (y1 - y0 + 2)
        data[(y * w + x) * c + ch] = Math.round(top + (bottom - top) * t)
      }
    }
  }
  const file = path.join(work, 'banner-clean.png')
  await sharp(data, { raw: { width: w, height: h, channels: c } }).png().toFile(file)
  return file
}
const jpg = (img, file) => img.jpeg({ quality: 88, mozjpeg: true }).toFile(file)
const cover = (src) => sharp(src).resize(W, H, { fit: 'cover', position: 'centre' })

const clean = await withoutTagline()
await jpg(cover(clean), path.join(repo, 'site/assets/brand/og-image.jpg'))
await jpg(cover(banner), path.join(repo, 'docs/marketing/images/en/01-launch.jpg'))

// the Persian one: the clean banner with a tagline under the name
const fonts = pathToFileURL(path.join(repo, 'site/assets/fonts')).href
const cleanCover = path.join(work, 'banner-clean-cover.png')
await cover(clean).png().toFile(cleanCover)
const html = `<!doctype html><html lang="fa" dir="rtl"><meta charset="utf-8"><style>
@font-face{font-family:Vazirmatn;font-weight:400;src:url(${fonts}/vazirmatn-arabic-400.woff2);unicode-range:U+0600-06FF,U+200C-200E,U+2010-2011,U+FB50-FDFF,U+FE70-FEFC}
@font-face{font-family:Inter;font-weight:400;src:url(${fonts}/inter-latin-400.woff2)}
*{margin:0;box-sizing:border-box}
body{width:${W}px;height:${H}px;background:url(${pathToFileURL(cleanCover).href}) 0 0/${W}px ${H}px no-repeat;position:relative;overflow:hidden}
.tag{position:absolute;left:600px;width:512px;top:352px;text-align:center;font:400 40px/1.4 Vazirmatn,Inter,sans-serif;color:#b7c1ce}
</style><body><div class="tag">فروشگاه VPN شما، روی تلگرام</div></body></html>`
const page = path.join(work, 'launch-fa.html')
fs.writeFileSync(page, html)
const browser = await puppeteer.launch({ executablePath: CHROME, headless: true, args: ['--allow-file-access-from-files', '--no-first-run'] })
const tab = await browser.newPage()
await tab.setViewport({ width: W, height: H, deviceScaleFactor: 1 })
await tab.goto(pathToFileURL(page).href, { waitUntil: 'networkidle0' })
await tab.evaluate(() => document.fonts.ready)
const shot = path.join(work, 'launch-fa.png')
await tab.screenshot({ path: shot })
await browser.close()
await jpg(sharp(shot), path.join(repo, 'docs/marketing/images/fa/01-launch.jpg'))
console.log('og-image.jpg, en/01-launch.jpg, fa/01-launch.jpg')
