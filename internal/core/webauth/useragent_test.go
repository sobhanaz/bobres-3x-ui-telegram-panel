package webauth

import "testing"

// A User-Agent header is reduced to a browser and a system from a fixed list, and anything else it says is dropped.
func TestDescribeUserAgent(t *testing.T) {
	for _, tc := range []struct{ name, ua, browser, system string }{
		{"chrome on windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36", BrowserChrome, SystemWindows},
		{"edge on windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0", BrowserEdge, SystemWindows},
		{"firefox on linux", "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0", BrowserFirefox, SystemLinux},
		{"safari on mac", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", BrowserSafari, SystemMacOS},
		{"safari on iphone", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", BrowserSafari, SystemIOS},
		{"chrome on iphone", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/126.0.6478.153 Mobile/15E148 Safari/604.1", BrowserChrome, SystemIOS},
		{"chrome on android", "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36", BrowserChrome, SystemAndroid},
		{"samsung browser", "Mozilla/5.0 (Linux; Android 13; SAMSUNG SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/24.0 Chrome/117.0.0.0 Mobile Safari/537.36", BrowserSamsung, SystemAndroid},
		{"opera", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 OPR/111.0.0.0", BrowserOpera, SystemWindows},
		{"chromebook", "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36", BrowserChrome, SystemChromeOS},
		{"a script", "curl/8.4.0", BrowserOther, SystemOther},
		{"nothing", "", BrowserOther, SystemOther},
	} {
		b, s := DescribeUserAgent(tc.ua)
		if b != tc.browser || s != tc.system {
			t.Errorf("%s: got %q on %q, want %q on %q", tc.name, b, s, tc.browser, tc.system)
		}
	}
	// Whatever else the header carries (markup, a link, a name) never comes back.
	b, s := DescribeUserAgent(`<a href="https://evil.example">Chrome/1</a> (Windows)`)
	if b != BrowserChrome || s != SystemWindows {
		t.Errorf("a header with markup: %q %q", b, s)
	}
}
