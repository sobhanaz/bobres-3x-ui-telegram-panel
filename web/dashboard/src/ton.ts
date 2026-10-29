// TON (Toncoin) addresses as staff type them: the shape, the CRC16 checksum, the
// network and the workchain, so a mistyped address is caught before customers
// send TON to it, and any form a wallet shows is turned into the one BOBRES
// stores. It mirrors Go's internal/tonaddr (core and payments check again).
// Pure functions (synchronous, no library).

// An address has two forms. The raw form is "0:<64 hex digits>". The
// user-friendly form is 48 characters of base64 (the standard or the URL-safe
// alphabet): a flag byte, the workchain, the 32-byte account and a CRC16 of
// those 34 bytes. "EQ…" is bounceable, "UQ…" non-bounceable (the test network
// adds 0x80 to the flag byte).

/** What is wrong with a typed address; '' when nothing. */
export type TonAddressProblem = '' | 'format' | 'checksum' | 'workchain' | 'testnet'

const tagBounceable = 0x11
const tagNonBounceable = 0x51
const tagTestOnly = 0x80

const std = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'
const url = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_'

/** cleanTonAddress drops what a copy and paste may add: spaces and invisible
 * direction marks. Base64 is case sensitive, so case stays. */
export function cleanTonAddress(raw: string): string {
  return raw.replace(/[\s\u200b-\u200f\u202a-\u202e\u2066-\u2069\ufeff]/g, '')
}

/** crc16 is CRC16-CCITT (XMODEM): polynomial 0x1021, initial value 0, no reflection. */
function crc16(bytes: Uint8Array): number {
  let crc = 0
  for (const b of bytes) {
    crc ^= b << 8
    for (let i = 0; i < 8; i++) crc = crc & 0x8000 ? ((crc << 1) ^ 0x1021) & 0xffff : (crc << 1) & 0xffff
  }
  return crc
}

/** base64Decode reads 48 characters as 36 bytes; null when a character is
 * not of one alphabet (the two are not mixed). */
function base64Decode(s: string): Uint8Array | null {
  const alphabet = /[-_]/.test(s) ? url : std
  const out = new Uint8Array((s.length / 4) * 3)
  for (let i = 0, o = 0; i < s.length; i += 4, o += 3) {
    let n = 0
    for (let j = 0; j < 4; j++) {
      const v = alphabet.indexOf(s[i + j])
      if (v < 0) return null
      n = (n << 6) | v
    }
    out[o] = (n >> 16) & 0xff
    out[o + 1] = (n >> 8) & 0xff
    out[o + 2] = n & 0xff
  }
  return out
}

function base64UrlEncode(bytes: Uint8Array): string {
  let out = ''
  for (let i = 0; i < bytes.length; i += 3) {
    const n = (bytes[i] << 16) | (bytes[i + 1] << 8) | bytes[i + 2]
    out += url[(n >> 18) & 63] + url[(n >> 12) & 63] + url[(n >> 6) & 63] + url[n & 63]
  }
  return out
}

const hexOf = (bytes: Uint8Array) => Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')

type Parsed = { hash: Uint8Array } | { problem: Exclude<TonAddressProblem, ''> }

function parseRaw(workchain: string, hex: string): Parsed {
  if (hex.length !== 64 || !/^[+-]?\d+$/.test(workchain)) return { problem: 'format' }
  if (Number(workchain) !== 0) return { problem: 'workchain' }
  if (!/^[0-9a-fA-F]{64}$/.test(hex)) return { problem: 'format' }
  return { hash: Uint8Array.from(hex.match(/../g)!, (h) => parseInt(h, 16)) }
}

function parseFriendly(s: string): Parsed {
  if (s.length !== 48) return { problem: 'format' }
  const raw = base64Decode(s)
  if (!raw) return { problem: 'format' }
  if (crc16(raw.subarray(0, 34)) !== ((raw[34] << 8) | raw[35])) return { problem: 'checksum' }
  const tag = raw[0] & ~tagTestOnly
  if (tag !== tagBounceable && tag !== tagNonBounceable) return { problem: 'format' }
  if (raw[1] !== 0) return { problem: 'workchain' }
  if (raw[0] & tagTestOnly) return { problem: 'testnet' }
  return { hash: raw.slice(2, 34) }
}

/** parse reads an address in either form; the test network and any workchain
 * but 0 are refused. The checks run in the order Go's tonaddr runs them. */
function parse(input: string): Parsed {
  const s = cleanTonAddress(input)
  if (!s) return { problem: 'format' }
  const colon = s.indexOf(':')
  return colon >= 0 ? parseRaw(s.slice(0, colon), s.slice(colon + 1)) : parseFriendly(s)
}

/** tonAddressProblem: '' when the address is right; format: not an address
 * (not 48 base64 characters, nor 0: and 64 hex digits); checksum: the right
 * shape, but a character is wrong; workchain: not the basechain (the
 * masterchain, -1, cannot receive payments here); testnet: an address of the
 * test network. */
export function tonAddressProblem(raw: string): TonAddressProblem {
  const p = parse(raw)
  return 'problem' in p ? p.problem : ''
}

/** canonicalTonAddress is the form BOBRES stores and shows customers:
 * non-bounceable ("UQ…"), URL-safe base64, checksum recomputed, main network.
 * A bounceable "EQ…", a raw "0:…" and a "UQ…" of one account give one
 * string. '' when the address is not right. */
export function canonicalTonAddress(raw: string): string {
  const p = parse(raw)
  if ('problem' in p) return ''
  const bytes = new Uint8Array(36)
  bytes[0] = tagNonBounceable
  bytes.set(p.hash, 2)
  const c = crc16(bytes.subarray(0, 34))
  bytes[34] = c >> 8
  bytes[35] = c & 0xff
  return base64UrlEncode(bytes)
}

/** rawTonAddress is the raw form "0:<hex>", the key two addresses of one
 * account share; '' when the address is not right. */
export function rawTonAddress(raw: string): string {
  const p = parse(raw)
  return 'problem' in p ? '' : '0:' + hexOf(p.hash)
}

/** normalizeTonAddress is the canonical form of a right address, else the
 * pasted text without what a paste adds (so the form shows what was typed). */
export function normalizeTonAddress(raw: string): string {
  return canonicalTonAddress(raw) || cleanTonAddress(raw)
}

/** sameTonAddress: both are right and name one account, whatever form each is in. */
export function sameTonAddress(a: string, b: string): boolean {
  const x = rawTonAddress(a)
  return x !== '' && x === rawTonAddress(b)
}
