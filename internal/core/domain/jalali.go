package domain

import (
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
)

// Jalali (Persian) calendar helpers for the sales reports. Dates here are the
// UTC midnights dateOf makes: calendar arithmetic with no zone. The conversion
// is i18n.GregorianToJalali, the same the bot uses to print a Persian date, so a
// report's months are the months staff read elsewhere.

// jalaliMonthStart is the Gregorian date of Jalali day 1 of d's Jalali month.
func jalaliMonthStart(d time.Time) time.Time {
	_, _, jd := i18n.GregorianToJalali(d.Year(), int(d.Month()), d.Day())
	return d.AddDate(0, 0, -(jd - 1))
}

// nextJalaliMonth is the Gregorian date of Jalali day 1 of the month after the
// one that starts on start. Every Jalali month has 29 to 31 days, so stepping
// from day 29 (start+28) reaches the next day 1 in at most three steps.
func nextJalaliMonth(start time.Time) time.Time {
	d := start.AddDate(0, 0, 28)
	for {
		if _, _, jd := i18n.GregorianToJalali(d.Year(), int(d.Month()), d.Day()); jd == 1 {
			return d
		}
		d = d.AddDate(0, 0, 1)
	}
}
