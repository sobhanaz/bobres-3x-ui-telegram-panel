import fs from 'node:fs'
import sharp from 'sharp'

// A made-up receipt picture: the demo customers have no Telegram files, so the dashboard's receipt requests get this.
fs.mkdirSync('out', { recursive: true })
const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="320" height="400" viewBox="0 0 320 400">
<rect width="320" height="400" fill="#f4f6f8"/>
<rect x="30" y="24" width="260" height="352" rx="10" fill="#ffffff" stroke="#d7dde4"/>
<circle cx="160" cy="70" r="22" fill="#14b8a6"/><path d="M150 70l7 7 14-15" stroke="#fff" stroke-width="5" fill="none" stroke-linecap="round" stroke-linejoin="round"/>
<text x="160" y="118" font-family="Helvetica,Arial,sans-serif" font-size="15" font-weight="700" fill="#0f172a" text-anchor="middle">Transfer complete</text>
<text x="160" y="140" font-family="Helvetica,Arial,sans-serif" font-size="11" fill="#64748b" text-anchor="middle">DEMO RECEIPT - not a real payment</text>
<g fill="#e2e8f0"><rect x="52" y="172" width="90" height="9" rx="4"/><rect x="52" y="212" width="70" height="9" rx="4"/><rect x="52" y="252" width="100" height="9" rx="4"/><rect x="52" y="292" width="80" height="9" rx="4"/></g>
<g fill="#94a3b8"><rect x="190" y="172" width="78" height="9" rx="4"/><rect x="206" y="212" width="62" height="9" rx="4"/><rect x="176" y="252" width="92" height="9" rx="4"/><rect x="196" y="292" width="72" height="9" rx="4"/></g>
<rect x="52" y="330" width="216" height="1.5" fill="#e2e8f0"/>
<rect x="52" y="346" width="120" height="10" rx="4" fill="#cbd5e1"/>
</svg>`
await sharp(Buffer.from(svg)).png().toFile('out/receipt.png')
console.log('ok')
