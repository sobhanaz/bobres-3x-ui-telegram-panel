import { describe, expect, it } from 'vitest'
import { createHash, randomBytes } from 'node:crypto'
import { base58Decode, normalizeTronAddress, sha256, tronAddressProblem } from '../tron'

const hex = (b: Uint8Array) => Buffer.from(b).toString('hex')
const nodeSha = (b: Uint8Array) => createHash('sha256').update(b).digest()

// An independent encoder, built on node's own sha256, to make addresses.
const alphabet = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz'
function base58check(payload: Uint8Array): string {
  const full = Buffer.concat([payload, nodeSha(nodeSha(payload)).subarray(0, 4)])
  let n = BigInt('0x' + full.toString('hex'))
  let out = ''
  while (n > 0n) {
    out = alphabet[Number(n % 58n)] + out
    n /= 58n
  }
  for (const b of full) {
    if (b !== 0) break
    out = '1' + out
  }
  return out
}
const tronAddress = () => base58check(Buffer.concat([Buffer.from([0x41]), randomBytes(20)]))

describe('sha256', () => {
  it('matches the published vectors', () => {
    expect(hex(sha256(new Uint8Array()))).toBe('e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855')
    expect(hex(sha256(new TextEncoder().encode('abc')))).toBe('ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad')
  })
  it('matches node for every length across the block edges', () => {
    for (let n = 0; n <= 200; n++) {
      const data = randomBytes(n)
      expect(hex(sha256(data)), String(n)).toBe(nodeSha(data).toString('hex'))
    }
  })
})

describe('TRON addresses', () => {
  it('takes real addresses', () => {
    // the USDT contract itself is a TRON address
    expect(tronAddressProblem('TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t')).toBe('')
    for (let i = 0; i < 50; i++) {
      const a = tronAddress()
      expect(a).toMatch(/^T/)
      expect(tronAddressProblem(a), a).toBe('')
      expect(base58Decode(a)).toHaveLength(25)
    }
  })
  it('catches a wrong or swapped character', () => {
    const a = 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t'
    for (let i = 1; i < a.length; i++) {
      const c = a[i] === 'z' ? 'y' : 'z'
      expect(tronAddressProblem(a.slice(0, i) + c + a.slice(i + 1)), String(i)).toBe('checksum')
    }
    expect(tronAddressProblem(a.slice(0, 5) + a[6] + a[5] + a.slice(7))).toBe('checksum')
  })
  it('refuses what is not shaped like one', () => {
    for (const bad of ['', 'T', 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6', 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6tt', 'XR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t', 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj0t', '0x' + 'a'.repeat(40)]) {
      expect(tronAddressProblem(bad), bad).toBe('format')
    }
    // a sound checksum and the right shape, but not a TRON account (version 0x42)
    const other = base58check(Buffer.concat([Buffer.from([0x42]), randomBytes(20)]))
    expect(other).toMatch(/^T.{33}$/)
    expect(tronAddressProblem(other)).toBe('checksum')
  })
  it('drops what a paste adds, and keeps the case', () => {
    expect(normalizeTronAddress(' TR7NHqjeKQxG TCi8q8ZY4pL8otSzgjLj6t‏\n')).toBe('TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t')
    expect(tronAddressProblem('⁦TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t⁩')).toBe('')
    expect(tronAddressProblem('tr7nhqjekqxgtci8q8zy4pl8otszgjlj6t')).toBe('format')
  })
})
