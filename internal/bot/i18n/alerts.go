package i18n

import (
	"sort"
	"strconv"
	"time"
)

// limitNames say what a license limit counts (the name of a
// license_limit alert), in each language.
var limitNames = map[string]map[string]string{
	"active_services": {"en": "active services", "fa": "سرویس فعال"},
}

// AlertArgs formats a system alert's values (events.SystemAlertEvent.Args)
// for T in lang: count, days, hours, minutes, percent and limit in the language's
// digits, free (bytes) as a size, date (unix seconds) as a day, and the name
// of a license limit in words (active_services). Any other value, or one
// that is not a number, is a name and goes through text (html.EscapeString
// for a Telegram message).
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
		case k == "name" && limitNames[v][lang] != "":
			v = text(limitNames[v][lang])
		case err != nil:
			v = text(v)
		case k == "count" || k == "days" || k == "hours" || k == "minutes" || k == "limit" || k == "percent":
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
