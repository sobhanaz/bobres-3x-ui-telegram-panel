package loginwire

import (
	"strings"
	"testing"
)

const (
	testID    = "0192f3a41b2c7d3e8f40516273849abc"
	testNonce = "Zm9vYmFyYmF6Zm9v"
)

// A button's data round-trips, fits Telegram's 64 bytes, and anything else is refused.
func TestCallbackData(t *testing.T) {
	for _, approve := range []bool{true, false} {
		data := CallbackData(approve, testID, testNonce)
		if len(data) > 64 {
			t.Fatalf("%q is %d bytes: Telegram takes 64", data, len(data))
		}
		verb, rest, ok := strings.Cut(data, ":")
		if !ok || verb != CallbackVerb {
			t.Fatalf("verb of %q", data)
		}
		gotApprove, id, nonce, ok := ParseCallback(rest)
		if !ok || gotApprove != approve || id != testID || nonce != testNonce {
			t.Fatalf("ParseCallback(%q) = %v %q %q %v", rest, gotApprove, id, nonce, ok)
		}
	}
	for _, bad := range []string{"", "y", "y:", "y:a", "y:a:", "y::b", "x:a:b", "Y:a:b", "y:a:b:c", ":a:b"} {
		if _, _, _, ok := ParseCallback(bad); ok {
			t.Errorf("ParseCallback(%q) accepted", bad)
		}
	}
	if !ID(testID) || ID(testID[:31]) || ID(testID+"0") || ID(strings.ToUpper(testID)) || ID("0192f3a4-1b2c-7d3e-8f40-516273849abc") {
		t.Error("ID")
	}
	if !Nonce(testNonce) || Nonce(testNonce[:15]) || Nonce(testNonce+"a") || Nonce("Zm9vYmFyYmF6Zm9!") {
		t.Error("Nonce")
	}
}

func validRequest() Request {
	return Request{ApprovalID: testID, Account: Account{Username: "adam", TelegramID: 871, Role: "admin"}, Method: "link", IP: "203.0.113.7",
		Browser: "chrome", System: "windows", At: 1792500000, ExpiresAt: 1792500300,
		Targets: []Target{{TelegramID: 871, Language: "en", Role: "self", Nonce: testNonce}, {TelegramID: 9000, Language: "fa", Role: "owner", Nonce: "AAAAAAAAAAAAAAAA"}}}
}

func TestRequestValid(t *testing.T) {
	if err := validRequest().Valid(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Request){
		"an id of the wrong shape":          func(r *Request) { r.ApprovalID = "abc" },
		"an unknown method":                 func(r *Request) { r.Method = "sms" },
		"an address that is no address":     func(r *Request) { r.IP = "<script>" },
		"no times":                          func(r *Request) { r.At = 0 },
		"a wait that ends before it begins": func(r *Request) { r.ExpiresAt = r.At },
		"an account with markup":            func(r *Request) { r.Account.Username = "<b>x" },
		"an account with no id":             func(r *Request) { r.Account.TelegramID = 0 },
		"an unknown role":                   func(r *Request) { r.Account.Role = "god" },
		"no targets":                        func(r *Request) { r.Targets = nil },
		"too many targets": func(r *Request) {
			for i := 0; i < MaxTargets; i++ {
				r.Targets = append(r.Targets, Target{TelegramID: int64(100 + i), Role: "owner", Nonce: testNonce})
			}
		},
		"a target twice":                func(r *Request) { r.Targets[1].TelegramID = r.Targets[0].TelegramID },
		"a target with no nonce":        func(r *Request) { r.Targets[0].Nonce = "" },
		"a nonce of the wrong shape":    func(r *Request) { r.Targets[0].Nonce = "short" },
		"a target with an unknown role": func(r *Request) { r.Targets[0].Role = "friend" },
		"a language that is a sentence": func(r *Request) { r.Targets[0].Language = "this is not a language" },
	} {
		r := validRequest()
		mutate(&r)
		if err := r.Valid(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// An address is optional (a request with none shows a dash), IPv6 is fine.
	r := validRequest()
	r.IP = ""
	if err := r.Valid(); err != nil {
		t.Errorf("no address: %v", err)
	}
	r.IP = "2001:db8::1"
	if err := r.Valid(); err != nil {
		t.Errorf("IPv6: %v", err)
	}
}

func TestResolutionAndNoticeValid(t *testing.T) {
	res := Resolution{ApprovalID: testID, Result: "approved", Account: Account{Username: "adam", TelegramID: 871, Role: "admin"},
		By: &Account{TelegramID: 9000, Role: "owner"}, At: 1792500100, Targets: []Target{{TelegramID: 9000, Language: "en", MessageID: 12}}}
	if err := res.Valid(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Resolution){
		"an unknown result":            func(r *Resolution) { r.Result = "maybe" },
		"a target with no message":     func(r *Resolution) { r.Targets[0].MessageID = 0 },
		"no time":                      func(r *Resolution) { r.At = 0 },
		"a decider that is no account": func(r *Resolution) { r.By.Role = "nobody" },
		"a bad id":                     func(r *Resolution) { r.ApprovalID = "x" },
	} {
		r := res
		by := *res.By
		r.By = &by
		r.Targets = append([]Target(nil), res.Targets...)
		mutate(&r)
		if err := r.Valid(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	n := Notice{Kind: "enabled", Source: "dashboard", By: &Account{Username: "boss", TelegramID: 9000, Role: "owner"}, Targets: []Target{{TelegramID: 9000, Language: "fa"}}}
	if err := n.Valid(); err != nil {
		t.Fatal(err)
	}
	n.By = nil
	n.Source = "server"
	n.Kind = "disabled"
	if err := n.Valid(); err != nil {
		t.Fatalf("the server's command line: %v", err)
	}
	for name, mutate := range map[string]func(*Notice){
		"an unknown kind":   func(n *Notice) { n.Kind = "changed" },
		"an unknown source": func(n *Notice) { n.Source = "email" },
		"no targets":        func(n *Notice) { n.Targets = nil },
	} {
		c := n
		c.Targets = append([]Target(nil), n.Targets...)
		mutate(&c)
		if err := c.Valid(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
