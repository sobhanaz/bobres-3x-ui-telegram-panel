// TRON (TRC20) addresses as staff type them: the shape and the base58check
// checksum, so a mistyped address is caught before customers send USDT to
// it. Payments checks again. Pure functions (synchronous, no library).
import { latinDigits } from './format'

const alphabet = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz'
/** T and 33 base58 characters (no 0, O, I or l), as core checks it */
const addressRe = /^T[1-9A-HJ-NP-Za-km-z]{33}$/

/** normalizeTronAddress drops what a copy and paste may add: spaces and
 * invisible direction marks. Base58 is case sensitive, so case stays. */
export function normalizeTronAddress(raw: string): string {
  return latinDigits(raw).replace(/[\s​-‏‪-‮⁦-⁩﻿]/g, '')
}

/** tronAddressProblem: '' when the address is right; format: not T and 33
 * base58 characters; checksum: the right shape, but a character is wrong. */
export function tronAddressProblem(raw: string): '' | 'format' | 'checksum' {
  const a = normalizeTronAddress(raw)
  if (!addressRe.test(a)) return 'format'
  const bytes = base58Decode(a)
  // 0x41, the 20-byte account, then the first 4 bytes of sha256(sha256(those 21))
  if (bytes.length !== 25 || bytes[0] !== 0x41) return 'checksum'
  const sum = sha256(sha256(bytes.subarray(0, 21)))
  for (let i = 0; i < 4; i++) if (sum[i] !== bytes[21 + i]) return 'checksum'
  return ''
}

/** base58Decode reads a string of base58 characters (already checked). */
export function base58Decode(s: string): Uint8Array {
  const out: number[] = [] // little endian
  for (const ch of s) {
    let carry = alphabet.indexOf(ch)
    for (let i = 0; i < out.length; i++) {
      carry += out[i] * 58
      out[i] = carry & 0xff
      carry >>= 8
    }
    for (; carry > 0; carry >>= 8) out.push(carry & 0xff)
  }
  // each leading '1' is a zero byte
  for (let i = 0; i < s.length && s[i] === '1'; i++) out.push(0)
  return Uint8Array.from(out.reverse())
}

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
])
const ror = (x: number, n: number) => (x >>> n) | (x << (32 - n))

/** sha256 of a short message (FIPS 180-4); the checksum needs nothing more. */
export function sha256(data: Uint8Array): Uint8Array {
  const h = new Uint32Array([0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19])
  const blocks = Math.ceil((data.length + 9) / 64)
  const buf = new Uint8Array(blocks * 64)
  buf.set(data)
  buf[data.length] = 0x80
  const view = new DataView(buf.buffer)
  view.setUint32(buf.length - 8, Math.floor(data.length / 0x20000000))
  view.setUint32(buf.length - 4, (data.length * 8) >>> 0)
  const w = new Uint32Array(64)
  for (let off = 0; off < buf.length; off += 64) {
    for (let i = 0; i < 16; i++) w[i] = view.getUint32(off + i * 4)
    for (let i = 16; i < 64; i++) {
      const s0 = ror(w[i - 15], 7) ^ ror(w[i - 15], 18) ^ (w[i - 15] >>> 3)
      const s1 = ror(w[i - 2], 17) ^ ror(w[i - 2], 19) ^ (w[i - 2] >>> 10)
      w[i] = w[i - 16] + s0 + w[i - 7] + s1
    }
    let [a, b, c, d, e, f, g, k] = h
    for (let i = 0; i < 64; i++) {
      const t1 = (k + (ror(e, 6) ^ ror(e, 11) ^ ror(e, 25)) + ((e & f) ^ (~e & g)) + K[i] + w[i]) | 0
      const t2 = ((ror(a, 2) ^ ror(a, 13) ^ ror(a, 22)) + ((a & b) ^ (a & c) ^ (b & c))) | 0
      k = g
      g = f
      f = e
      e = (d + t1) | 0
      d = c
      c = b
      b = a
      a = (t1 + t2) | 0
    }
    h[0] += a
    h[1] += b
    h[2] += c
    h[3] += d
    h[4] += e
    h[5] += f
    h[6] += g
    h[7] += k
  }
  const out = new Uint8Array(32)
  const ov = new DataView(out.buffer)
  h.forEach((x, i) => ov.setUint32(i * 4, x))
  return out
}
