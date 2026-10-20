// A small client for core's /api/v1: same-origin cookies for the session,
// and the session's CSRF token on every request that changes something.

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  /** The whole error answer, for extra fields such as field or problems. */
  readonly data: Record<string, unknown>
  constructor(status: number, code: string, message: string, data: Record<string, unknown> = {}) {
    super(message)
    this.status = status
    this.code = code
    this.data = data
  }
}

let csrfToken = ''
let onAuthLost: () => void = () => {}

export function setCsrf(token: string): void {
  csrfToken = token
}

/** Called when the server says the session ended (401 on a logged-in call). */
export function whenAuthLost(fn: () => void): void {
  onAuthLost = fn
}

/**
 * api calls core. A Blob or File body (an upload) is sent as it is, with its
 * own type; anything else is sent as JSON.
 */
export async function api<T>(method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  const raw = body instanceof Blob
  if (raw) headers['Content-Type'] = body.type || 'application/octet-stream'
  else if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET' && csrfToken) headers['X-CSRF-Token'] = csrfToken
  let res: Response
  try {
    res = await fetch('/api/v1' + path, {
      method,
      headers,
      credentials: 'same-origin',
      body: raw ? body : body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new ApiError(0, 'network', 'network')
  }
  if (res.status === 204) return undefined as T
  const data = (await res.json().catch(() => ({}))) as Record<string, unknown>
  if (!res.ok) {
    const code = typeof data.error === 'string' ? data.error : 'error'
    const message = typeof data.message === 'string' ? data.message : res.statusText
    if (res.status === 401 && !path.startsWith('/auth/')) onAuthLost()
    throw new ApiError(res.status, code, message, data)
  }
  return data as T
}

/**
 * upload sends a file as the raw body of a POST (type application/octet-stream),
 * the way the browser streams it from disk: nothing of it is read into the
 * page. onProgress hears how many bytes have gone; abort() stops it. The
 * answer and the errors are api()'s.
 */
export function upload<T>(path: string, body: Blob, onProgress?: (sent: number, total: number) => void): { done: Promise<T>; abort: () => void } {
  const xhr = new XMLHttpRequest()
  const done = new Promise<T>((resolve, reject) => {
    xhr.open('POST', '/api/v1' + path)
    xhr.setRequestHeader('Accept', 'application/json')
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')
    if (csrfToken) xhr.setRequestHeader('X-CSRF-Token', csrfToken)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress?.(e.loaded, e.total)
    }
    xhr.onerror = () => reject(new ApiError(0, 'network', 'network'))
    xhr.ontimeout = () => reject(new ApiError(0, 'network', 'network'))
    xhr.onabort = () => reject(new ApiError(0, 'aborted', 'aborted'))
    xhr.onload = () => {
      let data: Record<string, unknown> = {}
      try {
        data = JSON.parse(xhr.responseText || '{}') as Record<string, unknown>
      } catch {
        /* not JSON: the status says enough */
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(data as T)
        return
      }
      if (xhr.status === 401) onAuthLost()
      reject(new ApiError(xhr.status, typeof data.error === 'string' ? data.error : 'error', typeof data.message === 'string' ? data.message : xhr.statusText, data))
    }
    xhr.send(body)
  })
  return { done, abort: () => xhr.abort() }
}

/** query builds "?a=1&b=x" from the values that are set. */
export function query(params: Record<string, string | number | boolean | undefined | null>): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') q.set(k, String(v))
  }
  const s = q.toString()
  return s ? '?' + s : ''
}

/** newKey is a request key: a change sent twice with it happens once. */
export function newKey(): string {
  return crypto.randomUUID().replaceAll('-', '')
}
