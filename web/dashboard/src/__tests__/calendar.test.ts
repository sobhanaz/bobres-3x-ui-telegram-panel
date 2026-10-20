import { describe, expect, it } from 'vitest'
import {
  addDays,
  DATE_MAX,
  DATE_MIN,
  daysBetween,
  digits,
  firstWeekday,
  formatDate,
  formatInput,
  formatMonth,
  gregorianMonthLength,
  gregorianToJalali,
  inRange,
  isJalaliLeap,
  isoOf,
  jalaliMonthLength,
  jalaliToGregorian,
  monthGrid,
  parseInput,
  parseIso,
  partsOf,
  shiftMonth,
  todayIn,
  toIso,
  weekday,
  weekdayName,
  type Calendar,
} from '../calendar'
import { customProblem, validCustom } from '../reports'

// Every Gregorian day from `from` to `to` (both included) as year, month, day.
function* days(from: string, to: string) {
  for (let t = Date.parse(from); t <= Date.parse(to); t += 86_400_000) {
    const d = new Date(t)
    yield { y: d.getUTCFullYear(), m: d.getUTCMonth() + 1, d: d.getUTCDate(), iso: d.toISOString().slice(0, 10) }
  }
}

// 1 Farvardin of these Jalali years falls on these Gregorian days (the anchors core's own tests use too).
const nowruz: Record<number, string> = {
  1378: '1999-03-21', 1399: '2020-03-20', 1403: '2024-03-20', 1404: '2025-03-21', 1405: '2026-03-21', 1408: '2029-03-20',
  1412: '2033-03-20', 1416: '2037-03-20', 1420: '2041-03-20', 1479: '2100-03-21',
}

describe('Jalali and Gregorian dates, both ways', () => {
  it('converts the new year of each anchor year exactly, and the day before it is the last of the year before', () => {
    for (const [year, iso] of Object.entries(nowruz)) {
      const g = parseIso(iso)!
      expect(gregorianToJalali(g.y, g.m, g.d), iso).toEqual({ y: Number(year), m: 1, d: 1 })
      expect(isoOf('jalali', Number(year), 1, 1), year).toBe(iso)
      const before = parseIso(addDays(iso, -1))!
      const last = gregorianToJalali(before.y, before.m, before.d)
      expect(last, iso).toMatchObject({ y: Number(year) - 1, m: 12 })
      expect(last.d).toBe(jalaliMonthLength(Number(year) - 1, 12))
    }
  })

  it('knows which Jalali years are leap, and gives Esfand a 30th day only then', () => {
    // 1399, 1403 and 1408 are leap; 1404 to 1407 are not (the 33-year rule the official calendar follows)
    for (const y of [1399, 1403, 1408, 1412, 1416, 1420, 1424, 1428, 1432, 1436, 1441]) expect(isJalaliLeap(y), String(y)).toBe(true)
    for (const y of [1400, 1401, 1402, 1404, 1405, 1406, 1407, 1409, 1410, 1411, 1440]) expect(isJalaliLeap(y), String(y)).toBe(false)
    expect(isoOf('jalali', 1403, 12, 30)).toBe('2025-03-20') // the leap day
    expect(isoOf('jalali', 1403, 12, 29)).toBe('2025-03-19')
    expect(isoOf('jalali', 1404, 12, 30)).toBeNull() // a common year has no 30 Esfand
    expect(isoOf('jalali', 1404, 12, 29)).toBe('2026-03-20')
    expect(isoOf('jalali', 1405, 1, 1)).toBe('2026-03-21') // the day after it
  })

  it('gives every Jalali month its length: 31 for the first six, 30 for the next five, and Esfand 29 or 30', () => {
    for (const y of [1403, 1404]) {
      const lengths = Array.from({ length: 12 }, (_, i) => jalaliMonthLength(y, i + 1))
      expect(lengths.slice(0, 6)).toEqual([31, 31, 31, 31, 31, 31])
      expect(lengths.slice(6, 11)).toEqual([30, 30, 30, 30, 30])
      expect(lengths[11]).toBe(y === 1403 ? 30 : 29)
    }
    expect(isoOf('jalali', 1405, 6, 31)).not.toBeNull() // Shahrivar has 31
    expect(isoOf('jalali', 1405, 7, 31)).toBeNull() // Mehr has 30
    expect(isoOf('jalali', 1405, 7, 30)).toBe('2026-10-22')
    expect(isoOf('jalali', 1405, 8, 1)).toBe('2026-10-23')
    expect(isoOf('jalali', 1405, 13, 1)).toBeNull()
    expect(isoOf('jalali', 1405, 0, 1)).toBeNull()
    expect(isoOf('jalali', 1405, 1, 0)).toBeNull()
    expect(isoOf('jalali', 1405, 1.5, 1)).toBeNull()
  })

  it('gives every Gregorian month its length, with 29 February only in leap years', () => {
    expect([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12].map((m) => gregorianMonthLength(2026, m))).toEqual([31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31])
    expect(isoOf('gregorian', 2024, 2, 29)).toBe('2024-02-29')
    expect(isoOf('gregorian', 2026, 2, 29)).toBeNull()
    expect(isoOf('gregorian', 2100, 2, 29)).toBeNull() // 2100 is not a leap year
    expect(isoOf('gregorian', 2000, 2, 29)).toBe('2000-02-29') // 2000 is
    expect(isoOf('gregorian', 2026, 4, 31)).toBeNull()
    expect(isoOf('gregorian', 2026, 12, 31)).toBe('2026-12-31')
    expect(parseIso('2026-02-29')).toBeNull()
    expect(parseIso('2026-10-14')).toEqual({ y: 2026, m: 10, d: 14 })
    expect(parseIso('2026-1-4')).toBeNull()
    expect(parseIso('')).toBeNull()
  })

  it('round trips every day from 1900 to 2200, in both directions', () => {
    // A million assertions are slow: the loops only collect what is wrong.
    const wrong: string[] = []
    let n = 0
    for (const g of days('1900-01-01', '2200-12-31')) {
      const j = gregorianToJalali(g.y, g.m, g.d)
      const back = jalaliToGregorian(j.y, j.m, j.d)
      if (j.d < 1 || j.d > jalaliMonthLength(j.y, j.m) || back.y !== g.y || back.m !== g.m || back.d !== g.d) wrong.push(`${g.iso} -> ${j.y}/${j.m}/${j.d} -> ${back.y}-${back.m}-${back.d}`)
      n++
    }
    expect(n).toBe(109_938)
    expect(wrong.slice(0, 5)).toEqual([])
    // and every Jalali day of 1300 to 1500 back and forth, one day after the other
    let prev = ''
    for (let y = 1300; y <= 1500; y++) {
      for (let m = 1; m <= 12; m++) {
        for (let d = 1; d <= jalaliMonthLength(y, m); d++) {
          const iso = isoOf('jalali', y, m, d)
          const p = iso ? partsOf(iso, 'jalali') : null
          if (!iso || !p || p.y !== y || p.m !== m || p.d !== d) wrong.push(`${y}/${m}/${d} -> ${iso}`)
          else if (prev && addDays(prev, 1) !== iso) wrong.push(`${y}/${m}/${d}: ${prev} then ${iso}`) // no day skipped, none twice
          prev = iso ?? ''
        }
      }
    }
    expect(wrong.slice(0, 5)).toEqual([])
  }, 30_000)

  it('agrees with the browser\'s own Persian calendar for every day the API accepts (2000 to 2100)', () => {
    const f = new Intl.DateTimeFormat('en-US-u-ca-persian', { year: 'numeric', month: 'numeric', day: 'numeric', timeZone: 'UTC' })
    const wrong: string[] = []
    let n = 0
    for (const g of days(DATE_MIN, DATE_MAX)) {
      const p = Object.fromEntries(f.formatToParts(new Date(Date.UTC(g.y, g.m - 1, g.d))).filter((x) => x.type !== 'literal' && x.type !== 'era').map((x) => [x.type, Number(x.value)]))
      const mine = partsOf(g.iso, 'jalali')
      if (mine.y !== p.year || mine.m !== p.month || mine.d !== p.day) wrong.push(`${g.iso}: ${mine.y}/${mine.m}/${mine.d} but the browser says ${p.year}/${p.month}/${p.day}`)
      n++
    }
    expect(n).toBe(36_890) // 101 years, 25 of them leap
    expect(wrong.slice(0, 5)).toEqual([])
  }, 30_000)

  it('writes dates exactly as the browser does, in both calendars and both languages', () => {
    const cases: [string, Calendar, 'fa' | 'en', Intl.DateTimeFormatOptions, string][] = [
      ['fa', 'jalali', 'fa', { dateStyle: 'medium' }, 'fa-IR-u-ca-persian'],
      ['fa', 'gregorian', 'fa', { dateStyle: 'medium' }, 'fa-IR-u-ca-gregory'],
      ['en', 'gregorian', 'en', { dateStyle: 'medium' }, 'en-GB'],
      ['fa', 'jalali', 'fa', { month: 'short', day: 'numeric' }, 'fa-IR-u-ca-persian'],
      ['fa', 'gregorian', 'fa', { month: 'short', day: 'numeric' }, 'fa-IR-u-ca-gregory'],
      ['en', 'gregorian', 'en', { month: 'short', day: 'numeric' }, 'en-GB'],
    ]
    const wrong: string[] = []
    for (const [, calendar, lang, opts, locale] of cases) {
      const f = new Intl.DateTimeFormat(locale, { ...opts, timeZone: 'UTC' })
      const style = 'dateStyle' in opts ? 'medium' : 'short'
      for (const g of days('2025-01-01', '2027-12-31')) {
        const mine = formatDate(g.iso, lang, calendar, style)
        const theirs = f.format(new Date(Date.UTC(g.y, g.m - 1, g.d)))
        if (mine !== theirs) wrong.push(`${locale} ${style} ${g.iso}: ${mine} but the browser writes ${theirs}`)
      }
    }
    expect(wrong.slice(0, 5)).toEqual([])
    // English Jalali has no browser form without the era ("22 Mehr 1405 AP"): the same day, written like the Gregorian one
    expect(formatDate('2026-10-14', 'en', 'jalali')).toBe('22 Mehr 1405')
    expect(formatDate('2026-10-14', 'en', 'jalali', 'short')).toBe('22 Mehr')
    expect(formatDate('2026-10-14', 'en', 'gregorian')).toBe('14 Oct 2026')
    expect(formatDate('2026-10-14', 'fa', 'jalali')).toBe('۲۲ مهر ۱۴۰۵')
    expect(formatDate('2026-10-14', 'fa', 'gregorian')).toBe('۱۴ اکتبر ۲۰۲۶')
    expect(formatDate('not a date', 'en', 'jalali')).toBe('not a date')
    expect(formatMonth('2026-10-23', 'en', 'jalali')).toBe('Aban 1405')
    expect(formatMonth('2026-10-23', 'fa', 'jalali')).toBe('آبان ۱۴۰۵')
  })

  it('writes and reads the typed form back, for every day of the supported years, in either language and calendar', () => {
    for (const calendar of ['jalali', 'gregorian'] as const) {
      for (const lang of ['fa', 'en'] as const) {
        for (const g of days(DATE_MIN, DATE_MAX)) {
          const typed = formatInput(g.iso, lang, calendar)
          if (parseInput(typed, calendar) !== g.iso) throw new Error(`${calendar} ${lang} ${g.iso}: wrote ${typed}, read ${parseInput(typed, calendar)}`)
        }
      }
    }
    expect(formatInput('2026-10-14', 'fa', 'jalali')).toBe('۱۴۰۵/۰۷/۲۲')
    expect(formatInput('2026-10-14', 'en', 'jalali')).toBe('1405-07-22')
    expect(formatInput('2026-10-14', 'fa', 'gregorian')).toBe('۲۰۲۶/۱۰/۱۴')
    expect(formatInput('2026-10-14', 'en', 'gregorian')).toBe('2026-10-14')
    expect(formatInput('', 'en', 'gregorian')).toBe('')
  })

  it('reads a typed date in any digits and with any usual separator, and refuses what is no date', () => {
    const j = (s: string) => parseInput(s, 'jalali')
    const g = (s: string) => parseInput(s, 'gregorian')
    expect(j('1405/07/22')).toBe('2026-10-14')
    expect(j('۱۴۰۵/۰۷/۲۲')).toBe('2026-10-14')
    expect(j('١٤٠٥/٠٧/٢٢')).toBe('2026-10-14') // Arabic-Indic digits
    expect(j('1405-7-22')).toBe('2026-10-14')
    expect(j('1405.07.22')).toBe('2026-10-14')
    expect(j('1405 07 22')).toBe('2026-10-14')
    expect(j('1405،07،22')).toBe('2026-10-14') // the Persian comma
    expect(j('14050722')).toBe('2026-10-14')
    expect(j('  ۱۴۰۵/۷/۲۲  ')).toBe('2026-10-14')
    expect(j('\u200f۱۴۰۵/۰۷/۲۲\u200e')).toBe('2026-10-14') // directional marks that come with a pasted date
    expect(j('1405/1/1')).toBe('2026-03-21')
    expect(g('2026-10-14')).toBe('2026-10-14')
    expect(g('2026/10/14')).toBe('2026-10-14')
    expect(g('۲۰۲۶-۱۰-۱۴')).toBe('2026-10-14')
    expect(g('2026-2-9')).toBe('2026-02-09')
    // the same text means two days in two calendars
    expect(j('1405/07/22')).not.toBe(g('1405/07/22'))
    for (const bad of ['', ' ', '1405', '1405/07', '1405/07/', '1405/07/22/1', '05/07/22', '14050/07/22', '1405/007/22', '1405/07/022', '1405/13/01', '1405/00/10', '1405/07/00', '1405/07/31', '1404/12/30', 'abcd/ef/gh', '1405/07/2x', '-1405/07/22', '1405//07/22x']) {
      expect(j(bad), JSON.stringify(bad)).toBeNull()
    }
    expect(g('2026-02-29')).toBeNull()
    expect(g('2026-04-31')).toBeNull()
    expect(g('2026-13-01')).toBeNull()
    expect(j('1403/12/30')).toBe('2025-03-20')
    // a Gregorian date typed into a Jalali field is a (far) Jalali date, which the range check then refuses
    expect(inRange(j('2026-02-29')!)).toBe(false)
  })

  it('lays months out in weeks that start on Saturday (Jalali) or Monday (Gregorian), with no day missing or doubled', () => {
    expect(firstWeekday('jalali')).toBe(6)
    expect(firstWeekday('gregorian')).toBe(1)
    // Mehr 1405 starts on 2026-09-23, a Wednesday: Sat, Sun, Mon, Tue empty, then 30 days
    const mehr = monthGrid('jalali', 1405, 7)
    expect(weekday('2026-09-23')).toBe(3)
    expect(mehr[0]).toEqual([null, null, null, null, '2026-09-23', '2026-09-24', '2026-09-25'])
    expect(mehr.flat().filter(Boolean)).toHaveLength(30)
    expect(mehr.at(-1)!.filter(Boolean).at(-1)).toBe('2026-10-22')
    // October 2026 starts on a Thursday: Mon, Tue, Wed empty
    const oct = monthGrid('gregorian', 2026, 10)
    expect(oct[0]).toEqual([null, null, null, '2026-10-01', '2026-10-02', '2026-10-03', '2026-10-04'])
    expect(oct.flat().filter(Boolean)).toHaveLength(31)
    for (const w of [...mehr, ...oct]) expect(w).toHaveLength(7)
    // every month of both calendars over the supported years: all of its days, in order, each once, in rows of seven
    const wrong: string[] = []
    for (const calendar of ['jalali', 'gregorian'] as const) {
      const first = partsOf(DATE_MIN, calendar)
      let y = first.y
      let m = first.m
      const last = partsOf(DATE_MAX, calendar)
      let guard = 0
      while (y < last.y || (y === last.y && m <= last.m)) {
        const grid = monthGrid(calendar, y, m)
        const cells = grid.flat().filter((c): c is string => !!c)
        if (cells.length !== (calendar === 'jalali' ? jalaliMonthLength(y, m) : gregorianMonthLength(y, m))) wrong.push(`${calendar} ${y}/${m}: ${cells.length} days`)
        if (cells[0] !== isoOf(calendar, y, m, 1)) wrong.push(`${calendar} ${y}/${m}: starts on ${cells[0]}`)
        for (let i = 1; i < cells.length; i++) if (cells[i] !== addDays(cells[i - 1], 1)) wrong.push(`${calendar} ${y}/${m}: ${cells[i - 1]} then ${cells[i]}`)
        if (grid.some((w) => w.length !== 7)) wrong.push(`${calendar} ${y}/${m}: a week of other than seven`)
        ;({ y, m } = shiftMonth({ y, m }, 1))
        if (guard++ > 2000) throw new Error('runaway')
      }
    }
    expect(wrong.slice(0, 5)).toEqual([])
    expect(monthGrid('jalali', 1405, 13)).toEqual([])
  }, 30_000)

  it('moves by months across a year edge in both directions', () => {
    expect(shiftMonth({ y: 1405, m: 12 }, 1)).toEqual({ y: 1406, m: 1 })
    expect(shiftMonth({ y: 1405, m: 1 }, -1)).toEqual({ y: 1404, m: 12 })
    expect(shiftMonth({ y: 2026, m: 10 }, 12)).toEqual({ y: 2027, m: 10 })
    expect(shiftMonth({ y: 2026, m: 10 }, -24)).toEqual({ y: 2024, m: 10 })
    expect(shiftMonth({ y: 2026, m: 1 }, -13)).toEqual({ y: 2024, m: 12 })
  })

  it('counts days, weekdays and day steps without any time zone', () => {
    expect(daysBetween('2026-10-14', '2026-10-14')).toBe(0)
    expect(daysBetween('2026-10-14', '2026-10-15')).toBe(1)
    expect(daysBetween('2026-10-15', '2026-10-14')).toBe(-1)
    expect(daysBetween('2026-03-01', '2026-04-01')).toBe(31)
    expect(daysBetween('2026-01-01', '2027-02-04') + 1).toBe(400)
    // a spring-forward and a fall-back weekend in the United States are 24 hours long here
    expect(daysBetween('2026-03-07', '2026-03-09')).toBe(2)
    expect(daysBetween('2026-10-31', '2026-11-02')).toBe(2)
    expect(addDays('2026-02-28', 1)).toBe('2026-03-01')
    expect(addDays('2024-02-28', 1)).toBe('2024-02-29')
    expect(addDays('2026-01-01', -1)).toBe('2025-12-31')
    expect(addDays('2026-10-14', 400)).toBe('2027-11-18')
    expect(addDays('garbage', 1)).toBe('garbage')
    expect(weekday('2026-10-18')).toBe(0) // Sunday
    expect(weekday('2026-10-17')).toBe(6)
    expect(toIso({ y: 5, m: 3, d: 9 })).toBe('0005-03-09')
  })

  it('names the days of the week and the months', () => {
    expect([0, 1, 2, 3, 4, 5, 6].map((d) => weekdayName(d, 'en', 'narrow'))).toEqual(['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa'])
    expect([6, 0, 1, 2, 3, 4, 5].map((d) => weekdayName(d, 'fa', 'narrow'))).toEqual(['ش', 'ی', 'د', 'س', 'چ', 'پ', 'ج'])
    expect(weekdayName(6, 'fa', 'long')).toBe('شنبه')
    expect(weekdayName(1, 'en', 'long')).toBe('Monday')
    expect(digits('2026-10-14', 'fa')).toBe('۲۰۲۶-۱۰-۱۴')
    expect(digits('2026-10-14', 'en')).toBe('2026-10-14')
  })

  it('reads "today" in the store\'s time zone, not the browser\'s', () => {
    // 20:29 UTC on 31 October is 23:59 in Tehran (UTC+3:30), two minutes later it is 1 November there
    const at = (iso: string) => Date.parse(iso)
    expect(todayIn('Asia/Tehran', at('2026-10-31T20:29:00Z'))).toBe('2026-10-31')
    expect(todayIn('Asia/Tehran', at('2026-10-31T20:31:00Z'))).toBe('2026-11-01')
    expect(todayIn('UTC', at('2026-10-31T20:31:00Z'))).toBe('2026-10-31')
    expect(todayIn('America/Los_Angeles', at('2026-11-01T03:00:00Z'))).toBe('2026-10-31')
    expect(todayIn('Pacific/Kiritimati', at('2026-10-31T11:00:00Z'))).toBe('2026-11-01') // UTC+14
    // the day clocks go back in New York is 25 hours long: 04:30Z and 05:30Z are both 1 Nov 00:30
    expect(todayIn('America/New_York', at('2026-11-01T05:30:00Z'))).toBe('2026-11-01')
    // an unknown zone does not throw: it is the browser's day
    expect(todayIn('Not/AZone', at('2026-10-31T12:00:00Z'))).toMatch(/^\d{4}-\d{2}-\d{2}$/)
    expect(todayIn(undefined, at('2026-06-15T12:00:00Z'))).toMatch(/^2026-06-1[456]$/)
  })

  it('knows the supported years, and what is wrong with a custom range', () => {
    expect(DATE_MIN).toBe('2000-01-01')
    expect(DATE_MAX).toBe('2100-12-31')
    expect(inRange('2000-01-01')).toBe(true)
    expect(inRange('2100-12-31')).toBe(true)
    expect(inRange('1999-12-31')).toBe(false)
    expect(inRange('2101-01-01')).toBe(false)
    // their Jalali edges
    expect(partsOf(DATE_MIN, 'jalali')).toEqual({ y: 1378, m: 10, d: 11 })
    expect(partsOf(DATE_MAX, 'jalali')).toEqual({ y: 1479, m: 10, d: 10 })
    expect(customProblem('2026-10-01', '2026-10-14')).toBeNull()
    expect(customProblem('2026-10-14', '2026-10-14')).toBeNull()
    expect(customProblem('', '2026-10-14')).toBe('format')
    expect(customProblem('2026-10-01', '')).toBe('format')
    expect(customProblem('2026-02-30', '2026-03-01')).toBe('format') // not a day of the calendar at all
    expect(customProblem('1999-12-31', '2000-01-02')).toBe('range')
    expect(customProblem('2100-12-30', '2101-01-01')).toBe('range')
    expect(customProblem('2026-10-14', '2026-10-13')).toBe('order')
    expect(customProblem('2026-01-01', '2027-02-04')).toBeNull() // 400 days
    expect(customProblem('2026-01-01', '2027-02-05')).toBe('span') // 401
    expect(validCustom('2026-10-14', '2026-10-14')).toBe(true)
    expect(validCustom('2026-10-14', '2026-10-13')).toBe(false)
  })
})
