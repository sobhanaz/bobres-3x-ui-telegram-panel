import { afterEach, describe, expect, it, vi } from 'vitest'
import { MAX_RECEIPT, prepareReceipt, ReceiptError, SHRINK_ABOVE, SHRINK_SIDE, sniffImage } from '../receipt'
import { pngBytes } from './helpers'

const bytes = (...b: number[]) => new Uint8Array(b)
const file = (parts: BlobPart[], name: string, type: string) => new File(parts, name, { type })
const mb = (n: number) => Math.round(n * 1024 * 1024)

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('sniffImage', () => {
  it('knows a JPEG, a PNG and a WebP by their first bytes', () => {
    expect(sniffImage(bytes(0xff, 0xd8, 0xff, 0xe0, 0, 0))).toBe('jpeg')
    expect(sniffImage(pngBytes())).toBe('png')
    const webp = new TextEncoder().encode('RIFF\x24\x00\x00\x00WEBPVP8 ')
    expect(sniffImage(webp)).toBe('webp')
  })

  it('refuses everything else, whatever it claims to be', () => {
    const text = (s: string) => new TextEncoder().encode(s)
    expect(sniffImage(text('<!doctype html>'))).toBeNull()
    expect(sniffImage(text('<svg xmlns="http://www.w3.org/2000/svg"/>'))).toBeNull()
    expect(sniffImage(text('%PDF-1.7'))).toBeNull()
    expect(sniffImage(text('GIF89a'))).toBeNull()
    expect(sniffImage(text('RIFF\x24\x00\x00\x00WAVEfmt '))).toBeNull()
    expect(sniffImage(new Uint8Array(0))).toBeNull()
    expect(sniffImage(bytes(0xff, 0xd8))).toBeNull() // too short to be sure
    expect(sniffImage(bytes(0x89, 0x50, 0x4e, 0x47))).toBeNull()
  })
})

describe('prepareReceipt', () => {
  it('takes a small image as it is, under a name of its own, with a preview', async () => {
    const r = await prepareReceipt(file([pngBytes()], 'Ali Reza - home address.png', 'image/png'))
    expect(r.name).toBe('receipt.png')
    expect(r.type).toBe('image/png')
    expect(r.blob.type).toBe('image/png')
    expect(r.size).toBe(pngBytes().length)
    expect(r.preview.startsWith('data:image/png;base64,')).toBe(true)
  })

  it('goes by the bytes, not by the name or the type the browser guessed', async () => {
    const r = await prepareReceipt(file([pngBytes()], 'notes.txt', 'text/plain'))
    expect(r.name).toBe('receipt.png')
    expect(r.blob.type).toBe('image/png')
    const jpeg = await prepareReceipt(file([bytes(0xff, 0xd8, 0xff, 0xe0, 1, 2, 3)], 'x', ''))
    expect(jpeg.name).toBe('receipt.jpg')
    expect(jpeg.type).toBe('image/jpeg')
  })

  it('refuses what is not a JPEG, PNG or WebP image, or is empty', async () => {
    const problem = (f: Blob) => prepareReceipt(f).then(() => '', (e: unknown) => (e instanceof ReceiptError ? e.problem : 'other'))
    expect(await problem(file(['<html></html>'], 'receipt.png', 'image/png'))).toBe('type')
    expect(await problem(file(['%PDF-1.7 hello'], 'receipt.jpg', 'image/jpeg'))).toBe('type')
    expect(await problem(file([], 'receipt.png', 'image/png'))).toBe('empty')
  })

  it('refuses an image over the limit that cannot be made smaller', async () => {
    const big = file([pngBytes(), new Uint8Array(MAX_RECEIPT)], 'big.png', 'image/png')
    await expect(prepareReceipt(big)).rejects.toMatchObject({ problem: 'size' })
    // exactly the limit is fine (and it is not made smaller where nothing can)
    const exact = file([pngBytes(), new Uint8Array(MAX_RECEIPT - pngBytes().length)], 'exact.png', 'image/png')
    const r = await prepareReceipt(exact)
    expect(r.size).toBe(MAX_RECEIPT)
    expect(r.preview).toBe('') // too big to show
  })

  it('makes a big picture smaller: at most 2560 pixels on a side, as a JPEG', async () => {
    const drawn: Array<[number, number]> = []
    vi.stubGlobal('createImageBitmap', vi.fn(async () => ({ width: 4000, height: 3000, close: vi.fn() })))
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
      fillStyle: '',
      fillRect: vi.fn(),
      drawImage: (_b: unknown, _x: number, _y: number, w: number, h: number) => drawn.push([w, h]),
    } as unknown as CanvasRenderingContext2D)
    vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation((cb, type) => {
      expect(type).toBe('image/jpeg')
      cb(new Blob([new Uint8Array(300_000)], { type: 'image/jpeg' }))
    })
    const original = file([pngBytes(), new Uint8Array(mb(6))], 'photo.png', 'image/png')
    const r = await prepareReceipt(original)
    expect(drawn).toEqual([[SHRINK_SIDE, 1920]])
    expect(r.name).toBe('receipt.jpg')
    expect(r.type).toBe('image/jpeg')
    expect(r.size).toBe(300_000)
    expect(r.preview.startsWith('data:image/jpeg;base64,')).toBe(true)
  })

  it('makes a picture over the limit fit by shrinking it', async () => {
    vi.stubGlobal('createImageBitmap', vi.fn(async () => ({ width: 8000, height: 6000, close: vi.fn() })))
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({ fillRect: vi.fn(), drawImage: vi.fn() } as unknown as CanvasRenderingContext2D)
    vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation((cb) => cb(new Blob([new Uint8Array(900_000)], { type: 'image/jpeg' })))
    const r = await prepareReceipt(file([pngBytes(), new Uint8Array(mb(12))], 'huge.png', 'image/png'))
    expect(r.size).toBe(900_000)
  })

  it('leaves a small picture alone, and falls back to the original when shrinking cannot be done', async () => {
    const created = vi.fn()
    vi.stubGlobal('createImageBitmap', created)
    await prepareReceipt(file([pngBytes(), new Uint8Array(SHRINK_ABOVE - 1000)], 'small.png', 'image/png'))
    expect(created).not.toHaveBeenCalled()

    // no createImageBitmap at all
    vi.stubGlobal('createImageBitmap', undefined)
    const big = file([pngBytes(), new Uint8Array(mb(3))], 'big.png', 'image/png')
    expect((await prepareReceipt(big)).size).toBe(big.size)

    // a picture the browser cannot decode
    vi.stubGlobal('createImageBitmap', vi.fn(async () => Promise.reject(new Error('cannot decode'))))
    expect((await prepareReceipt(big)).size).toBe(big.size)

    // no canvas context
    vi.stubGlobal('createImageBitmap', vi.fn(async () => ({ width: 100, height: 100, close: vi.fn() })))
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null)
    expect((await prepareReceipt(big)).size).toBe(big.size)

    // a "smaller" picture that is not smaller is not used
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({ fillRect: vi.fn(), drawImage: vi.fn() } as unknown as CanvasRenderingContext2D)
    vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation((cb) => cb(new Blob([new Uint8Array(mb(4))], { type: 'image/jpeg' })))
    const r = await prepareReceipt(big)
    expect(r.size).toBe(big.size)
    expect(r.type).toBe('image/png')

    // toBlob gives nothing
    vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation((cb) => cb(null))
    expect((await prepareReceipt(big)).size).toBe(big.size)
  })
})
