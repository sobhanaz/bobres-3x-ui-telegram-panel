import { describe, expect, it } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'

// Every text the dashboard asks for with t('…') or te('…') exists in both
// catalogs. A key that is missing renders as its own path (or, behind te(),
// silently takes the fallback), and nothing else notices: a cleanup of the
// catalogs once removed a key a component still used.
//
// This is a static scan of src/**/*.vue and *.ts (the tests aside). It reads
// the first argument of every call to t, te and $t:
//   - a literal ('a.b', "a.b", `a.b`) is a whole key and must exist;
//   - a literal an expression builds on ('a.b.' + x, `a.b.${x}`) is a prefix:
//     some key must start with it;
//   - the arms of a conditional (t(ok ? 'a.b' : 'a.c')) are whole keys;
//   - anything else (a variable, a call) is built at run time and out of
//     reach: it is listed in DYNAMIC_OK below when it is known to be fine.
// A key can also travel in a variable before it reaches t() (LicenseActivate
// picks 'license.activate.not_configured' in one function and shows it in
// another), so a second scan looks at every string literal that is shaped
// like a key of a catalog branch that exists, and finds the leaf missing.
const sources = import.meta.glob('../**/*.{vue,ts}', { query: '?raw', import: 'default', eager: true }) as Record<string, string>

// Keys the scan cannot see, each with where it is used and why it is good.
// (Nothing needs listing today: a key built from a variable is not read.)
const DYNAMIC_OK = new Set<string>([])

function flat(o: Record<string, unknown>, prefix = ''): string[] {
  return Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v as Record<string, unknown>, prefix + k + '.') : [prefix + k]))
}

interface Literal {
  raw: string
  plusBefore: boolean
  plusAfter: boolean
}

// The string literals at the top level of the first argument of the call
// whose "(" is just before start.
function firstArgument(src: string, start: number): Literal[] {
  const out: Literal[] = []
  let depth = 0
  let prev = ''
  for (let j = start; j < src.length; ) {
    const c = src[j]
    if (c === "'" || c === '"' || c === '`') {
      let k = j + 1
      let raw = ''
      let braces = 0
      for (; k < src.length; k++) {
        const d = src[k]
        if (d === '\\') {
          raw += src[++k]
          continue
        }
        if (c === '`' && d === '$' && src[k + 1] === '{') braces++
        if (braces > 0 && d === '}') braces--
        if (d === c && braces === 0) break
        raw += d
      }
      let after = k + 1
      while (/\s/.test(src[after] ?? '')) after++
      if (depth === 0) out.push({ raw, plusBefore: prev === '+', plusAfter: src[after] === '+' })
      prev = c
      j = k + 1
      continue
    }
    if ('([{'.includes(c)) depth++
    else if (')]}'.includes(c)) {
      if (depth === 0) break
      depth--
    } else if (c === ',' && depth === 0) break
    // the literals before a conditional's "?" are what it compares, not keys
    else if (c === '?' && depth === 0 && src[j + 1] !== '.' && src[j + 1] !== '?' && prev !== '?') out.length = 0
    if (!/\s/.test(c)) prev = c
    j++
  }
  return out
}

/** usedKeys: the keys (whole) and the key prefixes the sources ask for. */
function usedKeys(files: Record<string, string>) {
  const whole = new Map<string, string>()
  const prefixes = new Map<string, string>()
  for (const [file, src] of Object.entries(files)) {
    if (file.includes('/__tests__/') || file.endsWith('.d.ts')) continue
    for (const m of src.matchAll(/(?<![\w$.])(?:\$t|t|te)\(/g)) {
      const lits = firstArgument(src, m.index + m[0].length)
      for (const l of lits) {
        const dynamic = l.raw.includes('${')
        if (dynamic || l.plusAfter) {
          const prefix = dynamic ? l.raw.slice(0, l.raw.indexOf('${')) : l.raw
          if (prefix && !l.plusBefore) prefixes.set(prefix, file)
        } else if (!l.plusBefore && /^[\w-]+(\.[\w-]+)*$/.test(l.raw)) {
          whole.set(l.raw, file)
        }
      }
    }
  }
  return { whole, prefixes }
}

const quoted = /'([^'\\\n]*)'|"([^"\\\n]*)"|`([^`\\$\n]*)`/g

/** keyShaped: the string literals that look like a leaf of a catalog branch
 * (at least three words, the first a top-level group, the parent a group that
 * exists), whatever they are used for. A literal that ends where a variable
 * part starts ('a.b.c_' + x) is a prefix, which the other scan reads. */
function keyShaped(files: Record<string, string>, catalog: Record<string, unknown>) {
  const at = (path: string[]) => path.reduce<unknown>((o, k) => (o && typeof o === 'object' ? (o as Record<string, unknown>)[k] : undefined), catalog)
  const out = new Map<string, string>()
  for (const [file, src] of Object.entries(files)) {
    if (file.includes('/__tests__/') || file.endsWith('.d.ts')) continue
    for (const m of src.matchAll(quoted)) {
      const lit = m[1] ?? m[2] ?? m[3]
      if (!/^[a-z][a-z0-9_]*(\.[a-z0-9_]+){2,}$/.test(lit) || /[_.-]$/.test(lit)) continue
      const path = lit.split('.')
      const parent = at(path.slice(0, -1))
      if (parent && typeof parent === 'object' && at(path) === undefined) out.set(lit, file)
    }
  }
  return out
}

describe('the texts the sources ask for', () => {
  const { whole, prefixes } = usedKeys(sources)
  const catalogs = [
    ['fa', new Set(flat(fa))],
    ['en', new Set(flat(en))],
  ] as const

  it('reads the calls it should (the scan itself)', () => {
    expect(whole.size).toBeGreaterThan(1000)
    expect(prefixes.size).toBeGreaterThan(10)
    // a few that are known: a whole key, one behind te(), a ternary arm, a template, a concatenation
    expect(whole.has('system.https.load_error')).toBe(true)
    expect(whole.has('system.check_ok')).toBe(true)
    expect(whole.has('system.check_fail')).toBe(true)
    expect(prefixes.has('settings.invalid.')).toBe(true)
    expect(prefixes.has('system.https.card.')).toBe(true)
    // it understands the shapes it claims to
    const sample = usedKeys({
      '../x.vue': "t('a.one') te(\"a.two\") t(`a.three`) t(ok ? 'a.four' : 'a.five', { n: 1 }) t('a.six.' + x + '.title') t(`a.seven.${x}`) t(pick('a.skip'), 2) t(variable) foo.t('a.skip') format('a.skip')",
    })
    expect([...sample.whole.keys()].sort()).toEqual(['a.five', 'a.four', 'a.one', 'a.three', 'a.two'])
    expect([...sample.prefixes.keys()].sort()).toEqual(['a.seven.', 'a.six.'])
    // the key that went missing once travels in a variable: the second scan sees it
    const without = structuredClone(en)
    delete (without.license.activate as Record<string, unknown>).not_configured
    expect([...keyShaped(sources, without).keys()]).toEqual(['license.activate.not_configured'])
    // a conditional's comparison is not a key
    expect([...usedKeys({ '../y.vue': "t(state === 'unknown' ? 'b.one' : 'b.two') t(a?.b ? 'b.three' : 'b.four')" }).whole.keys()]).toEqual(['b.one', 'b.two', 'b.three', 'b.four'])
    // a key that travels in a variable is still seen, once it is missing from a branch that exists
    const cat = { a: { b: { here: 'x' } } }
    expect([...keyShaped({ '../z.ts': "const k = ok ? 'a.b.gone' : 'a.b.here'; const p = 'a.b.pre_' + n; const q = 'x.y.z'" }, cat).keys()]).toEqual(['a.b.gone'])
  })

  for (const [lang, keys] of catalogs) {
    it(`finds every key in the ${lang} catalog`, () => {
      const missing = [...whole].filter(([k]) => !keys.has(k) && !DYNAMIC_OK.has(k)).map(([k, file]) => `${k} (${file})`)
      expect(missing, `missing from ${lang}.json`).toEqual([])
    })
    it(`finds every key-shaped literal next to the ${lang} catalog's keys`, () => {
      const missing = [...keyShaped(sources, lang === 'fa' ? fa : en)].filter(([k]) => !DYNAMIC_OK.has(k)).map(([k, file]) => `${k} (${file})`)
      expect(missing, `missing from ${lang}.json`).toEqual([])
    })
    it(`has something under every key prefix a source builds on in the ${lang} catalog`, () => {
      const all = [...keys]
      const missing = [...prefixes].filter(([p]) => !all.some((k) => k.startsWith(p)) && !DYNAMIC_OK.has(p)).map(([p, file]) => `${p} (${file})`)
      expect(missing, `no key under these prefixes in ${lang}.json`).toEqual([])
    })
  }
})
