package webauth

import "strings"

// The browsers and systems DescribeUserAgent tells apart. They are codes, not words: the bot writes the names (a brand's name is the same in
// every language, "unknown" is not).
const (
	BrowserChrome  = "chrome"
	BrowserFirefox = "firefox"
	BrowserSafari  = "safari"
	BrowserEdge    = "edge"
	BrowserOpera   = "opera"
	BrowserSamsung = "samsung"
	BrowserOther   = "other"

	SystemWindows  = "windows"
	SystemMacOS    = "macos"
	SystemLinux    = "linux"
	SystemAndroid  = "android"
	SystemIOS      = "ios"
	SystemChromeOS = "chromeos"
	SystemOther    = "other"
)

// DescribeUserAgent reduces a User-Agent header to a browser and an operating system from the lists above. The header is whatever the client
// sent, so nothing of it is passed on: an approver reads only what this returns, which cannot hold markup, a link or a lie longer than a code.
func DescribeUserAgent(ua string) (browser, system string) {
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(ua, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("Edg/", "EdgA/", "EdgiOS/", "Edge/"):
		browser = BrowserEdge
	case has("OPR/", "Opera", "OPT/"):
		browser = BrowserOpera
	case has("SamsungBrowser/"):
		browser = BrowserSamsung
	case has("Firefox/", "FxiOS/"):
		browser = BrowserFirefox
	case has("Chrome/", "CriOS/", "Chromium/"):
		browser = BrowserChrome
	case has("Safari/"):
		browser = BrowserSafari
	default:
		browser = BrowserOther
	}
	switch {
	case has("Windows"):
		system = SystemWindows
	case has("Android"):
		system = SystemAndroid
	case has("iPhone", "iPad", "iPod"): // before macOS: their headers say "like Mac OS X"
		system = SystemIOS
	case has("Macintosh", "Mac OS X"):
		system = SystemMacOS
	case has("CrOS"):
		system = SystemChromeOS
	case has("Linux", "X11"):
		system = SystemLinux
	default:
		system = SystemOther
	}
	return browser, system
}
