// A small client for core's /api/v1: same-origin cookies for the session,
// and the session's CSRF token on every request that changes something.

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
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

export async function api<T>(method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET' && csrfToken) headers['X-CSRF-Token'] = csrfToken
  let res: Response
  try {
    res = await fetch('/api/v1' + path, {
      method,
      headers,
      credentials: 'same-origin',
      body: body === undefined ? undefined : JSON.stringify(body),
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
    throw new ApiError(res.status, code, message)
  }
  return data as T
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
