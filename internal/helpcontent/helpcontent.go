// Package helpcontent holds the few rules the bot's help screens (the FAQ under
// Support and the Tutorials menu), core (which stores and checks what the owner
// writes) and the dashboard's editor have to agree on: the two kinds of entry,
// the limits on what is written, and when the whole FAQ is shown as one message.
// Nothing here talks to a database or to Telegram.
package helpcontent

import "unicode/utf16"

// The two kinds of help entry.
const (
	// KindFAQ is a question with its answer.
	KindFAQ = "faq"
	// KindTutorial is a titled text with optional URL buttons (an app's download
	// link, for one).
	KindTutorial = "tutorial"
)

// Kinds lists the kinds in the order the dashboard shows them.
var Kinds = []string{KindFAQ, KindTutorial}

// IsKind reports whether k is a kind of help entry.
func IsKind(k string) bool { return k == KindFAQ || k == KindTutorial }

// Limits on what an owner writes. A length is counted in UTF-16 code units, the
// way Telegram counts a message; the tags of a text do not count.
const (
	// MaxFAQ and MaxTutorials are how many entries of each kind a store keeps.
	MaxFAQ       = 50
	MaxTutorials = 30
	// MaxQuestion is a question's length; MaxTutorialTitle a tutorial's title,
	// which is a button's label in the bot's list.
	MaxQuestion      = 200
	MaxTutorialTitle = 64
	// MaxBody is an answer's or a tutorial text's length. With a title of the
	// longest size and the frame the bot puts around them the answer screen is
	// well within Telegram's 4096.
	MaxBody = 3000
	// MaxButtons is how many URL buttons a tutorial has, MaxButtonLabel the length
	// of a label and MaxURL of an address.
	MaxButtons     = 8
	MaxButtonLabel = 64
	MaxURL         = 1024
)

// MaxEntries is how many entries of a kind a store keeps (0 for an unknown
// kind).
func MaxEntries(kind string) int {
	switch kind {
	case KindFAQ:
		return MaxFAQ
	case KindTutorial:
		return MaxTutorials
	}
	return 0
}

// MaxTitle is the longest title of a kind of entry (0 for an unknown kind).
func MaxTitle(kind string) int {
	switch kind {
	case KindFAQ:
		return MaxQuestion
	case KindTutorial:
		return MaxTutorialTitle
	}
	return 0
}

// The FAQ is shown as one numbered message, every question with its answer,
// while it has at most OneMessageMaxEntries entries and their questions and
// answers together take at most OneMessageMaxUnits. Past either it becomes a
// numbered list of the questions, each opening its answer. The sums are
// chosen so that the single message, with its heading, its footer and the
// numbers, stays within Telegram's 4096.
const (
	OneMessageMaxEntries = 12
	OneMessageMaxUnits   = 3000
)

// Units counts a plain text the way Telegram counts a message's length: in
// UTF-16 code units.
func Units(s string) int { return len(utf16.Encode([]rune(s))) }

// FitsOneMessage reports whether a FAQ of entries entries, whose questions and
// answers take units in all, is shown as one message.
func FitsOneMessage(entries, units int) bool {
	return entries > 0 && entries <= OneMessageMaxEntries && units <= OneMessageMaxUnits
}
