// What a reseller types for a service: a name and a private note. Each is one
// line; core checks the same limits and refuses anything else.

/** MAX_LABEL is the longest name of a service, in characters (core's domain.MaxLabel). */
export const MAX_LABEL = 64

/** MAX_NOTE is the longest private note on a service, in characters (core's domain.MaxNote). */
export const MAX_NOTE = 200

/** oneLine turns runs of spaces and line breaks into one space and trims the ends. */
export function oneLine(s: string): string {
  return s.replace(/\s+/g, ' ').trim()
}

/** length counts characters the way core does (code points, not UTF-16 units). */
export const length = (s: string): number => [...s].length

/** cleanLabel is a service's name as it is sent; null when it is too long (an empty name is fine: it removes the name). */
export function cleanLabel(s: string): string | null {
  const v = oneLine(s)
  return length(v) > MAX_LABEL ? null : v
}

/** cleanNote is a service's note as it is sent; null when it is too long (an empty note removes it). */
export function cleanNote(s: string): string | null {
  const v = oneLine(s)
  return length(v) > MAX_NOTE ? null : v
}
