// Converts the raw dashboard screenshots (out/raw) to the WebP files the site uses (site/assets/shots).
import fs from 'node:fs'
import sharp from 'sharp'
const WT = new URL('../../../', import.meta.url).pathname
const out = WT + 'site/assets/shots/'
for (const f of fs.readdirSync('out/raw').filter((f) => f.endsWith('.png'))) {
  let img = sharp('out/raw/' + f)
  const name = f.replace(/\.png$/, '')
  if (name.endsWith('-backups')) img = img.extract({ left: 0, top: 0, width: 1152, height: 412 })
  const q = name.endsWith('-m') ? 80 : 84
  await img.webp({ quality: q, effort: 6 }).toFile(out + 'dash-' + name + '.webp')
}
for (const f of fs.readdirSync(out)) console.log(f, (fs.statSync(out + f).size / 1024).toFixed(0) + ' KB')
