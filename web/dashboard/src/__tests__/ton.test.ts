import { describe, expect, it } from 'vitest'
import { randomBytes } from 'node:crypto'
import { canonicalTonAddress, cleanTonAddress, normalizeTonAddress, rawTonAddress, sameTonAddress, tonAddressProblem } from '../ton'

// Addresses published in TON's documentation (public data, not keys): one account in the raw form and in the
// bounceable friendly form.
const docRaw = '0:ed1691307050047117b998b561d8de82d31fbf84910ced6eb5fc92e7485ef8a7'
const docBounceable = 'EQDtFpEwcFAEcRe5mLVh2N6C0x-_hJEM7W61_JLnSF74p4q2'
const foundationRaw = '0:83dfd552e63729b472fcbcc8c45ebcc6691702558b68ec7527e1ba403a0f31a8'
const foundationBounceable = 'EQCD39VS5jcptHL8vMjEXrzGaRcCVYto7HUn4bpAOg8xqB2N'
// The same two accounts in the non-bounceable form (flag byte 0x51, checksum recomputed): what BOBRES stores.
const docCanonical = 'UQDtFpEwcFAEcRe5mLVh2N6C0x-_hJEM7W61_JLnSF74p9dz'
const foundationCanonical = 'UQCD39VS5jcptHL8vMjEXrzGaRcCVYto7HUn4bpAOg8xqEBI'

// An independent builder (table-free CRC16 over node's own base64) to make addresses of any tag and workchain.
function crc16(bytes: Uint8Array): number {
  let crc = 0
  for (const b of bytes) {
    crc = (crc ^ (b << 8)) & 0xffff
    for (let i = 0; i < 8; i++) crc = (crc & 0x8000 ? (crc << 1) ^ 0x1021 : crc << 1) & 0xffff
  }
  return crc
}
function build(tag: number, workchain: number, hash: Uint8Array = Uint8Array.from({ length: 32 }, (_, i) => i + 1)): string {
  const raw = Buffer.alloc(36)
  raw[0] = tag
  raw[1] = workchain
  Buffer.from(hash).copy(raw, 2)
  raw.writeUInt16BE(crc16(raw.subarray(0, 34)), 34)
  return raw.toString('base64url')
}
const standard = (s: string) => s.replace(/-/g, '+').replace(/_/g, '/')

describe('TON addresses', () => {
  it('reads the published addresses in every form as one account', () => {
    expect(tonAddressProblem(docRaw)).toBe('')
    expect(tonAddressProblem(docBounceable)).toBe('')
    expect(rawTonAddress(docBounceable)).toBe(docRaw)
    expect(rawTonAddress(foundationBounceable)).toBe(foundationRaw)
    expect(rawTonAddress(docRaw)).toBe(docRaw)
    // the non-bounceable form of the same account: UQ, and it reads back as the same account
    const canonical = canonicalTonAddress(docBounceable)
    expect(canonical).toMatch(/^UQ[A-Za-z0-9_-]{46}$/)
    expect(tonAddressProblem(canonical)).toBe('')
    expect(rawTonAddress(canonical)).toBe(docRaw)
    expect(sameTonAddress(docRaw, docBounceable)).toBe(true)
    expect(sameTonAddress(canonical, docBounceable)).toBe(true)
    expect(sameTonAddress(docRaw, canonical)).toBe(true)
    expect(sameTonAddress(docRaw, foundationRaw)).toBe(false)
    expect(sameTonAddress(docBounceable, foundationBounceable)).toBe(false)
  })

  it('turns a bounceable EQ, a raw 0: and a UQ of one account into the same canonical string', () => {
    const canonical = canonicalTonAddress(docRaw)
    expect(canonical).toBe(docCanonical)
    expect(canonicalTonAddress(docBounceable)).toBe(docCanonical)
    expect(canonicalTonAddress(docCanonical)).toBe(docCanonical)
    expect(canonicalTonAddress(foundationRaw)).toBe(foundationCanonical)
    expect(canonicalTonAddress(foundationBounceable)).toBe(foundationCanonical)
    expect(canonical).toBe(canonicalTonAddress(canonical))
    // the canonical form is the bounceable one with the flag byte 0x51 and a new checksum
    expect(Buffer.from(canonical, 'base64url')[0]).toBe(0x51)
    expect(Buffer.from(canonical, 'base64url').subarray(2, 34).toString('hex')).toBe(docRaw.slice(2))
    expect(canonical).toBe(build(0x51, 0, Buffer.from(docRaw.slice(2), 'hex')))
    expect(canonicalTonAddress(foundationRaw)).toBe(canonicalTonAddress(foundationBounceable))
    expect(canonicalTonAddress(foundationRaw)).not.toBe(canonical)
    expect(canonicalTonAddress(foundationRaw)).toBe(build(0x51, 0, Buffer.from(foundationRaw.slice(2), 'hex')))
    // URL-safe: never + or /, and 48 characters without padding
    for (let i = 0; i < 200; i++) {
      const a = canonicalTonAddress('0:' + randomBytes(32).toString('hex'))
      expect(a).toMatch(/^UQ[A-Za-z0-9_-]{46}$/)
      expect(tonAddressProblem(a)).toBe('')
    }
  })

  it('takes the standard alphabet as well as the URL-safe one', () => {
    const std = standard(docBounceable)
    expect(std).toContain('+')
    expect(std).toContain('/')
    expect(tonAddressProblem(std)).toBe('')
    expect(rawTonAddress(std)).toBe(docRaw)
    expect(canonicalTonAddress(std)).toBe(canonicalTonAddress(docBounceable))
    // the two alphabets are not mixed
    expect(tonAddressProblem(docBounceable.replace('x-_', 'x-/'))).toBe('format')
    expect(tonAddressProblem(standard(docBounceable).replace('x+/', 'x+_'))).toBe('format')
    // every random account survives both alphabets and both tags
    for (let i = 0; i < 100; i++) {
      const hash = randomBytes(32)
      for (const tag of [0x11, 0x51]) {
        const url = build(tag, 0, hash)
        expect(tonAddressProblem(url), url).toBe('')
        expect(tonAddressProblem(standard(url)), url).toBe('')
        expect(rawTonAddress(url)).toBe('0:' + hash.toString('hex'))
        expect(rawTonAddress(standard(url))).toBe('0:' + hash.toString('hex'))
      }
    }
  })

  it('drops what a paste adds around an address, and keeps the case', () => {
    for (const pasted of [
      ' ' + docBounceable + '\n',
      '‎' + docBounceable + '‏',
      '⁦' + docBounceable + '⁩',
      docBounceable.slice(0, 10) + ' ' + docBounceable.slice(10),
      ' ' + docBounceable.slice(0, 24) + '\t' + docBounceable.slice(24) + '﻿',
      ' ' + docRaw + ' ',
    ]) {
      expect(tonAddressProblem(pasted), JSON.stringify(pasted)).toBe('')
      expect(rawTonAddress(pasted), JSON.stringify(pasted)).toBe(docRaw)
    }
    expect(cleanTonAddress(' EQ Dt‏\n')).toBe('EQDt')
    // base64 is case sensitive: a lower-cased address is another one, and a wrong one
    expect(tonAddressProblem(docBounceable.toLowerCase())).not.toBe('')
    // the hex of a raw address is not
    expect(tonAddressProblem(docRaw.toUpperCase())).toBe('')
    expect(rawTonAddress(docRaw.toUpperCase())).toBe(docRaw)
  })

  it('refuses what is not shaped like an address', () => {
    for (const bad of [
      '',
      '   ',
      'not an address',
      docBounceable.slice(0, 47),
      docBounceable + 'A',
      '!'.repeat(48),
      docBounceable.slice(0, 47) + '=',
      'TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r', // a TRON address
      '0x0000000000000000000000000000000000000001', // an Ethereum address
      '0:' + 'ab'.repeat(31), // raw with a short hash
      '0:' + 'zz'.repeat(32), // raw with a bad hash
      'x:' + 'ab'.repeat(32), // raw with no workchain number
      ':' + 'ab'.repeat(32),
      '0:' + 'ab'.repeat(33),
      docBounceable.slice(0, 20) + 'é' + docBounceable.slice(21),
    ]) {
      expect(tonAddressProblem(bad), JSON.stringify(bad)).toBe('format')
      expect(canonicalTonAddress(bad), JSON.stringify(bad)).toBe('')
      expect(rawTonAddress(bad), JSON.stringify(bad)).toBe('')
    }
    // an unknown flag byte with a sound checksum is not an address either
    expect(tonAddressProblem(build(0x22, 0))).toBe('format')
    expect(tonAddressProblem(build(0x00, 0))).toBe('format')
  })

  it('catches a wrong, swapped or lost character by the checksum', () => {
    for (let i = 0; i < docBounceable.length; i++) {
      const c = docBounceable[i] === 'Z' ? 'Y' : 'Z'
      expect(tonAddressProblem(docBounceable.slice(0, i) + c + docBounceable.slice(i + 1)), String(i)).toBe('checksum')
    }
    expect(tonAddressProblem(docBounceable.slice(0, 10) + docBounceable[11] + docBounceable[10] + docBounceable.slice(12))).toBe('checksum')
    // a lost character is the wrong length, not a checksum
    expect(tonAddressProblem(docBounceable.slice(0, 20) + docBounceable.slice(21))).toBe('format')
  })

  it('refuses the test network, with its own problem', () => {
    // 0Q… (non-bounceable) and kQ… (bounceable) of the test network, with sound checksums
    const testNonBounceable = build(0x51 | 0x80, 0)
    const testBounceable = build(0x11 | 0x80, 0)
    expect(testNonBounceable).toMatch(/^0Q/)
    expect(testBounceable).toMatch(/^kQ/)
    expect(tonAddressProblem(testNonBounceable)).toBe('testnet')
    expect(tonAddressProblem(testBounceable)).toBe('testnet')
    expect(tonAddressProblem(standard(testBounceable))).toBe('testnet')
    expect(canonicalTonAddress(testBounceable)).toBe('')
    // the same bytes for the main network are fine
    expect(tonAddressProblem(build(0x51, 0))).toBe('')
    expect(tonAddressProblem(build(0x11, 0))).toBe('')
    // a test network address whose checksum is wrong is a checksum problem first
    expect(tonAddressProblem(testBounceable.slice(0, 20) + (testBounceable[20] === 'Z' ? 'Y' : 'Z') + testBounceable.slice(21))).toBe('checksum')
  })

  it('refuses every workchain but 0, with its own problem', () => {
    expect(tonAddressProblem(build(0x11, 0xff))).toBe('workchain') // the masterchain, -1
    expect(tonAddressProblem(build(0x51, 0xff))).toBe('workchain')
    expect(tonAddressProblem(build(0x11, 1))).toBe('workchain')
    expect(tonAddressProblem('-1:' + 'ab'.repeat(32))).toBe('workchain')
    expect(tonAddressProblem('1:' + 'ab'.repeat(32))).toBe('workchain')
    expect(tonAddressProblem('+1:' + 'ab'.repeat(32))).toBe('workchain')
    expect(tonAddressProblem('0:' + 'ab'.repeat(32))).toBe('')
    expect(tonAddressProblem('00:' + 'ab'.repeat(32))).toBe('')
    expect(canonicalTonAddress(build(0x11, 0xff))).toBe('')
    expect(sameTonAddress(build(0x11, 0xff), build(0x11, 0xff))).toBe(false)
    // a masterchain address on the test network is reported for the workchain, as Go's tonaddr does
    expect(tonAddressProblem(build(0x91, 0xff))).toBe('workchain')
  })

  it('never calls two invalid addresses the same account', () => {
    expect(sameTonAddress('', '')).toBe(false)
    expect(sameTonAddress('x', 'x')).toBe(false)
    expect(sameTonAddress(docRaw, '')).toBe(false)
  })

  it('normalizes to the canonical form, and to the pasted text without its decoration when it is wrong', () => {
    expect(normalizeTonAddress(' ' + docBounceable + '‏')).toBe(canonicalTonAddress(docBounceable))
    expect(normalizeTonAddress(docRaw)).toBe(canonicalTonAddress(docBounceable))
    const typo = docBounceable.slice(0, 20) + 'Z' + docBounceable.slice(21)
    expect(normalizeTonAddress(' ' + typo + ' ')).toBe(typo)
    expect(normalizeTonAddress('')).toBe('')
    expect(normalizeTonAddress('not an address')).toBe('notanaddress')
  })
})
