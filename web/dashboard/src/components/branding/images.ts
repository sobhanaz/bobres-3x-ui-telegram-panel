// Reading a picked image in the browser, for the logo and the bot photo.

/** readDataUrl reads a file as a data: URL (the dashboard's CSP allows no
 * blob: images). */
export function readDataUrl(f: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result))
    r.onerror = () => reject(r.error)
    r.readAsDataURL(f)
  })
}

/** readHead reads the first n bytes of a file. */
export function readHead(f: Blob, n: number): Promise<Uint8Array> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(new Uint8Array(r.result as ArrayBuffer))
    r.onerror = () => reject(r.error)
    r.readAsArrayBuffer(f.slice(0, n))
  })
}

/** imageSize loads an image and gives its pixel size. */
export function imageSize(url: string): Promise<{ width: number; height: number }> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => resolve({ width: img.naturalWidth, height: img.naturalHeight })
    img.onerror = () => reject(new Error('unreadable'))
    img.src = url
  })
}
