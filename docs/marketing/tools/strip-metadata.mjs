// Takes the metadata out of PNG and JPEG files in place, without re-encoding them (the pixels do not change):
// EXIF, XMP, IPTC, text chunks and content credentials. Image generators leave such tags (a credit line that
// names the generator, for one); a public asset should not carry them. Colour data (ICC profile, gamma, JFIF,
// Adobe transform) stays.
//
//   node strip-metadata.mjs <file or folder>...
import fs from 'node:fs'
import path from 'node:path'

const PNG_SIG = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
const PNG_KEEP = new Set(['IHDR', 'PLTE', 'IDAT', 'IEND', 'tRNS', 'gAMA', 'cHRM', 'sRGB', 'iCCP', 'pHYs', 'sBIT', 'bKGD', 'acTL', 'fcTL', 'fdAT'])

function stripPng(buf) {
  const out = [PNG_SIG]
  let p = 8
  while (p < buf.length) {
    const len = buf.readUInt32BE(p)
    const type = buf.toString('latin1', p + 4, p + 8)
    const end = p + 12 + len
    if (PNG_KEEP.has(type)) out.push(buf.subarray(p, end))
    p = end
  }
  return Buffer.concat(out)
}

function stripJpeg(buf) {
  const out = [buf.subarray(0, 2)] // SOI
  let p = 2
  while (p < buf.length) {
    if (buf[p] !== 0xff) throw new Error('bad JPEG marker at ' + p)
    const marker = buf[p + 1]
    if (marker === 0xda) { out.push(buf.subarray(p)); break } // start of scan: the rest is image data
    const len = buf.readUInt16BE(p + 2)
    const seg = buf.subarray(p, p + 2 + len)
    // APP1 EXIF/XMP, APP11 JUMBF (content credentials), APP12, APP13 Photoshop/IPTC, APP15, COM
    const drop = marker === 0xe1 || marker === 0xeb || marker === 0xec || marker === 0xed || marker === 0xef || marker === 0xfe
    if (!drop) out.push(seg)
    p += 2 + len
  }
  return Buffer.concat(out)
}

function strip(file) {
  const buf = fs.readFileSync(file)
  let out
  if (buf.subarray(0, 8).equals(PNG_SIG)) out = stripPng(buf)
  else if (buf[0] === 0xff && buf[1] === 0xd8) out = stripJpeg(buf)
  else return console.log('skipped (not PNG or JPEG):', file)
  if (out.length === buf.length) return console.log('clean already:', file)
  fs.writeFileSync(file, out)
  console.log('stripped', file, buf.length, '->', out.length, 'bytes')
}

for (const arg of process.argv.slice(2)) {
  const walk = (p) => {
    if (fs.statSync(p).isDirectory()) fs.readdirSync(p).forEach((n) => n !== 'node_modules' && walk(path.join(p, n)))
    else if (/\.(png|jpe?g)$/i.test(p)) strip(p)
  }
  walk(arg)
}
