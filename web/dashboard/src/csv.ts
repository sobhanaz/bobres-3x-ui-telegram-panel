// CSV files the dashboard itself writes (the files core streams, such as
// ledger.csv, are written in Go: reportText in internal/core/web does the same
// job there). One place for the two things every CSV needs: quoting, and a
// guard against a cell a spreadsheet would run as a formula.

/** The byte-order mark that makes spreadsheet programs read UTF-8 (Persian) right. */
export const BOM = String.fromCharCode(0xfeff)

const formulaStart = /^[\t\r\n\v\f ]*[=+\-@]/
const tabOrReturnStart = /^[\t\r]/

/** safeText keeps text somebody typed from running as a formula: a cell that
 * is, or after spaces and line breaks becomes, one (it starts with =, +, - or
 * @, or with a tab or a carriage return) gets a quote in front, which
 * spreadsheet programs show as text. */
export function safeText(s: string): string {
  return formulaStart.test(s) || tabOrReturnStart.test(s) ? "'" + s : s
}

/** csvCell writes one cell: numbers as they are, text through safeText, and
 * quoted when it holds a comma, a quote or a line break. */
export function csvCell(v: string | number): string {
  const s = typeof v === 'number' ? String(v) : safeText(v)
  return /[",\r\n]/.test(s) ? '"' + s.replaceAll('"', '""') + '"' : s
}

/** csvFile is rows as the text of a CSV file: UTF-8 with a byte-order mark,
 * CRLF line ends. */
export function csvFile(rows: ReadonlyArray<ReadonlyArray<string | number>>): string {
  return BOM + rows.map((r) => r.map(csvCell).join(',')).join('\r\n') + '\r\n'
}

/** download hands a text to the browser as a file (nothing is sent anywhere). */
export function download(name: string, text: string, type = 'text/csv;charset=utf-8'): void {
  const url = URL.createObjectURL(new Blob([text], { type }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** copyText puts text on the clipboard, with the old selection trick where the
 * clipboard API is not there (plain http, an old browser); false when neither
 * worked. */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // fall through to the selection trick
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.insetBlockStart = '0'
  area.style.opacity = '0'
  document.body.appendChild(area)
  area.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  area.remove()
  return ok
}
