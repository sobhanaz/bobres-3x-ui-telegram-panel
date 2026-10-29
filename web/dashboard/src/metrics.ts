// Helpers for the Prometheus card of Settings > Monitoring: what a metrics token may look like, and the scrape
// configuration the card shows for it. Core checks the token again; this only spares a round trip.
// Pure functions (no API, no Vue).

/** The length a token of the owner's own may have (core's MetricsTokenMin and MetricsTokenMax). */
export const TOKEN_MIN = 24
export const TOKEN_MAX = 128

/** The characters of a bearer token (RFC 6750): letters, digits and . _ ~ + / - =. */
const TOKEN_CHARS = /^[A-Za-z0-9._~+/=-]+$/

export type TokenProblem = '' | 'short' | 'long' | 'chars'

/** tokenProblem says what is wrong with a token typed by hand; '' when nothing is. */
export function tokenProblem(raw: string): TokenProblem {
  const v = raw.trim()
  if (v.length < TOKEN_MIN) return 'short'
  if (v.length > TOKEN_MAX) return 'long'
  return TOKEN_CHARS.test(v) ? '' : 'chars'
}

/** metricsAddress is the address Prometheus reads, on the store's own domain. */
export const metricsAddress = (origin: string): string => origin.replace(/\/+$/, '') + '/metrics'

/** scrapeConfig is the job to add to prometheus.yml. The token goes in a file on the Prometheus server, never in the
 * configuration itself. The host is the dashboard's own, without a scheme. */
export function scrapeConfig(host: string): string {
  return [
    'scrape_configs:',
    '  - job_name: bobres',
    '    scheme: https',
    '    metrics_path: /metrics',
    '    scrape_interval: 30s',
    '    authorization:',
    '      type: Bearer',
    '      credentials_file: /etc/prometheus/bobres-metrics-token',
    '    static_configs:',
    `      - targets: ["${host}"]`,
    '',
  ].join('\n')
}

/** MetricsState is GET /metrics-token: whether /metrics answers (a token is set). The token itself is never sent back. */
export interface MetricsState {
  enabled: boolean
}

/** The answer of making a token: the token, shown once. */
export interface MadeToken extends MetricsState {
  token: string
}
