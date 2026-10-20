package helpcontent

import "testing"

func TestKindsAndTheirLimits(t *testing.T) {
	if len(Kinds) != 2 || Kinds[0] != KindFAQ || Kinds[1] != KindTutorial {
		t.Fatalf("kinds: %v", Kinds)
	}
	for _, k := range Kinds {
		if !IsKind(k) || MaxEntries(k) == 0 || MaxTitle(k) == 0 {
			t.Errorf("kind %q: %v %d %d", k, IsKind(k), MaxEntries(k), MaxTitle(k))
		}
	}
	for _, k := range []string{"", "FAQ", "news", "faq ", "tutorials"} {
		if IsKind(k) || MaxEntries(k) != 0 || MaxTitle(k) != 0 {
			t.Errorf("%q is taken for a kind", k)
		}
	}
	if MaxEntries(KindFAQ) != MaxFAQ || MaxEntries(KindTutorial) != MaxTutorials || MaxTitle(KindFAQ) != MaxQuestion || MaxTitle(KindTutorial) != MaxTutorialTitle {
		t.Error("the limits of a kind are not the constants")
	}
}

// Telegram counts a message in UTF-16 units: a letter outside the first plane
// (an emoji) is two, the half-space of Persian is one.
func TestUnitsAreUTF16(t *testing.T) {
	for s, want := range map[string]int{"": 0, "abc": 3, "سلام": 4, "می\u200cخواهم": 8, "\U0001F600": 2, "a\U0001F600b": 4, "\U0001F468\u200d\U0001F469\u200d\U0001F467": 8, "é": 1} {
		if got := Units(s); got != want {
			t.Errorf("Units(%q) = %d, want %d", s, got, want)
		}
	}
}

// The FAQ is one message up to twelve questions and three thousand units, and a
// list past either.
func TestFitsOneMessageAtTheEdges(t *testing.T) {
	for _, c := range []struct {
		entries, units int
		want           bool
	}{
		{0, 0, false}, // nothing to show is not a message
		{1, 0, true},
		{1, OneMessageMaxUnits, true},
		{1, OneMessageMaxUnits + 1, false},
		{OneMessageMaxEntries, 100, true},
		{OneMessageMaxEntries, OneMessageMaxUnits, true},
		{OneMessageMaxEntries, OneMessageMaxUnits + 1, false},
		{OneMessageMaxEntries + 1, 10, false},
		{OneMessageMaxEntries + 1, OneMessageMaxUnits + 1, false},
	} {
		if got := FitsOneMessage(c.entries, c.units); got != c.want {
			t.Errorf("FitsOneMessage(%d, %d) = %v, want %v", c.entries, c.units, got, c.want)
		}
	}
}

// The limits are chosen so that what the bot builds from them is inside
// Telegram's 4096 units: a guard for whoever raises one.
func TestLimitsLeaveRoomInATelegramMessage(t *testing.T) {
	const telegram = 4096
	const bold, numbering, line = 12, 6, 4 // the tags do not count, but a number, a dot and the breaks do
	// The one message: the heading and the footer (a hundred and fifty units each at most, by default;
	// the store may reword them), and a number and two line breaks for each question.
	single := OneMessageMaxUnits + 2*150 + OneMessageMaxEntries*(numbering+line)
	if single >= telegram {
		t.Errorf("one message of the FAQ can reach %d units", single)
	}
	// One answer on its screen: the longest question, the longest text, the mark and the breaks.
	if answer := MaxQuestion + MaxBody + bold; answer >= telegram {
		t.Errorf("an answer screen can reach %d units", answer)
	}
	// A page of the list of questions: ten of the longest, their numbers, the heading, the hint, the page and the footer.
	if list := 10*(MaxQuestion+numbering+line) + 4*150; list >= telegram {
		t.Errorf("a page of the list can reach %d units", list)
	}
	if MaxButtons > 20 || MaxButtonLabel > 64 || MaxURL > 2048 {
		t.Errorf("a tutorial's buttons are past what Telegram takes: %d buttons, %d units, %d bytes", MaxButtons, MaxButtonLabel, MaxURL)
	}
}
