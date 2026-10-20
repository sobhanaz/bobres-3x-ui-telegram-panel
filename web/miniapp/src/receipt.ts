// The receipt image of a manual payment, prepared on the phone before it is sent: checked by what the file
// really is (never its name or the type the browser guessed), and made smaller when it is big. The server checks
// again and has the last word (JPEG, PNG or WebP, at most 8 MB); this is for a clear message before an upload,
// and for a phone photo of 12 MB to reach it at all over a slow connection.

/** MAX_RECEIPT is the largest receipt the server takes, in bytes. */
export const MAX_RECEIPT = 8 * 1024 * 1024

/** Above this size a picture is made smaller before it is sent. */
export const SHRINK_ABOVE = 1.5 * 1024 * 1024
/** The longest side a shrunk picture has, in pixels: a bank's receipt stays readable. */
export const SHRINK_SIDE = 2560
/** A preview is only made for a picture up to this size (it is a data: URL, held in memory). */
const PREVIEW_UP_TO = 2 * 1024 * 1024

export type ImageKind = 'jpeg' | 'png' | 'webp'

/** sniffImage says what an image file is by its first bytes: null for anything but a JPEG, PNG or WebP. */
export function sniffImage(head: Uint8Array): ImageKind | null {
  const is = (offset: number, ...bytes: number[]) => bytes.every((b, i) => head[offset + i] === b)
  if (is(0, 0xff, 0xd8, 0xff)) return 'jpeg'
  if (is(0, 0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a)) return 'png'
  // RIFF....WEBP
  if (is(0, 0x52, 0x49, 0x46, 0x46) && is(8, 0x57, 0x45, 0x42, 0x50)) return 'webp'
  return null
}

const MIME: Record<ImageKind, string> = { jpeg: 'image/jpeg', png: 'image/png', webp: 'image/webp' }
const EXT: Record<ImageKind, string> = { jpeg: 'jpg', png: 'png', webp: 'webp' }

/** Why a file cannot be a receipt: it is not a JPEG, PNG or WebP image, it is empty, or it is too big to send even made smaller. */
export class ReceiptError extends Error {
  constructor(public problem: 'type' | 'empty' | 'size') {
    super(problem)
  }
}

/** A receipt ready to send. */
export interface PreparedReceipt {
  blob: Blob
  /** the name it is sent under: never the file's own, which can say who and where */
  name: string
  type: string
  size: number
  /** a data: URL to show it by ('' when it is too big to preview) */
  preview: string
}

function readBytes(blob: Blob): Promise<Uint8Array> {
  if (typeof blob.arrayBuffer === 'function') return blob.arrayBuffer().then((b) => new Uint8Array(b))
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(new Uint8Array(r.result as ArrayBuffer))
    r.onerror = () => reject(r.error)
    r.readAsArrayBuffer(blob)
  })
}

function dataURL(blob: Blob): Promise<string> {
  return new Promise((resolve) => {
    const r = new FileReader()
    r.onload = () => resolve(typeof r.result === 'string' ? r.result : '')
    r.onerror = () => resolve('')
    r.readAsDataURL(blob)
  })
}

/**
 * shrink draws the picture smaller on a canvas and returns it as a JPEG, or null where this browser cannot (no canvas,
 * a picture it cannot decode). A transparent PNG is put on white, as a JPEG has no transparency.
 */
export async function shrink(blob: Blob): Promise<Blob | null> {
  if (typeof createImageBitmap !== 'function' || typeof document === 'undefined') return null
  try {
    const bitmap = await createImageBitmap(blob, { imageOrientation: 'from-image' })
    const scale = Math.min(1, SHRINK_SIDE / Math.max(bitmap.width, bitmap.height))
    const canvas = document.createElement('canvas')
    canvas.width = Math.max(1, Math.round(bitmap.width * scale))
    canvas.height = Math.max(1, Math.round(bitmap.height * scale))
    const ctx = canvas.getContext('2d')
    if (!ctx) return null
    ctx.fillStyle = '#ffffff'
    ctx.fillRect(0, 0, canvas.width, canvas.height)
    ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height)
    bitmap.close?.()
    return await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/jpeg', 0.88))
  } catch {
    return null
  }
}

/**
 * prepareReceipt checks a chosen file and makes it ready to send. A file that is not a JPEG, PNG or WebP image (by its
 * bytes) or is empty is refused; a big one is made smaller when the browser can, and refused when it cannot and it is
 * over the server's limit.
 */
export async function prepareReceipt(file: Blob): Promise<PreparedReceipt> {
  if (file.size === 0) throw new ReceiptError('empty')
  const kind = sniffImage(await readBytes(file.slice(0, 16)))
  if (!kind) throw new ReceiptError('type')
  let blob: Blob = file.type === MIME[kind] ? file : file.slice(0, file.size, MIME[kind])
  let out = kind
  if (file.size > SHRINK_ABOVE) {
    const smaller = await shrink(blob)
    if (smaller && smaller.size > 0 && smaller.size < file.size) {
      blob = smaller
      out = 'jpeg'
    }
  }
  if (blob.size > MAX_RECEIPT) throw new ReceiptError('size')
  return {
    blob,
    name: `receipt.${EXT[out]}`,
    type: MIME[out],
    size: blob.size,
    preview: blob.size <= PREVIEW_UP_TO ? await dataURL(blob) : '',
  }
}
