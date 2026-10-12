package i18n

import (
	"sort"
	"strconv"
	"time"
)

// AlertArgs formats a system alert's values (events.SystemAlertEvent.Args)
// for T in lang: count, days, hours and minutes in the language's digits,
// free (bytes) as a size, date (unix seconds) as a day. Any other value, or
// one that is not a number, is a name and goes through text
// (html.EscapeString for a Telegram message).
func AlertArgs(lang string, args map[string]string, text func(string) string) []any {
	names := make([]string, 0, len(args))
	for k := range args {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]any, 0, 2*len(names))
	for _, k := range names {
		v := args[k]
		n, err := strconv.ParseInt(v, 10, 64)
		switch {
		case err != nil:
			v = text(v)
		case k == "count" || k == "days" || k == "hours" || k == "minutes":
			v = Number(lang, n)
		case k == "free":
			v = FileSize(lang, n)
		case k == "date":
			v = Date(lang, time.Unix(n, 0))
		default:
			v = text(v)
		}
		out = append(out, k, v)
	}
	return out
}
