// Converts the chat renderer's PNG files (the folder chat/render.mjs wrote) to the WebP files the site uses
// (site/assets/chat). Pass names to convert only some:
//
//   node convert-chat.mjs <png-dir> [checkout-en checkout-fa ...]
import fs from 'node:fs'
import path from 'node:path'
import sharp from 'sharp'
const dir = path.resolve(process.argv[2] || 'chat/out')
const only = process.argv.slice(3)
const out = new URL('../../../site/assets/chat/', import.meta.url).pathname
for (const f of fs.readdirSync(dir).filter((f) => f.endsWith('.png'))) {
  const name = f.replace(/\.png$/, '')
  if (only.length && !only.includes(name)) continue
  await sharp(path.join(dir, f)).webp({ quality: 84, alphaQuality: 90, effort: 6 }).toFile(out + 'chat-' + name + '.webp')
  console.log('chat-' + name + '.webp', (fs.statSync(out + 'chat-' + name + '.webp').size / 1024).toFixed(0) + ' KB')
}
