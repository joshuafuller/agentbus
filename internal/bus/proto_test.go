package bus

import "testing"

func TestHelloRoundTrip(t *testing.T) {
	name, oneshot, ok := ParseHello(Hello("codex-1"))
	if !ok || name != "codex-1" || oneshot {
		t.Fatalf("got %q oneshot=%v ok=%v, want codex-1 false true", name, oneshot, ok)
	}
}

func TestHelloOneshotRoundTrip(t *testing.T) {
	name, oneshot, ok := ParseHello(HelloOneshot("tester"))
	if !ok || name != "tester" || !oneshot {
		t.Fatalf("got %q oneshot=%v ok=%v, want tester true true", name, oneshot, ok)
	}
}

func TestParseHelloRejects(t *testing.T) {
	for _, line := range []string{"HELLO ", "HELLO [x]", "hello bob", "[a] hi", "HELLO", "HELLO two words"} {
		if name, _, ok := ParseHello(line); ok {
			t.Errorf("ParseHello(%q) accepted as %q, want reject", line, name)
		}
	}
}

func TestMessageRoundTrip(t *testing.T) {
	from, text, ok := ParseMessage(Message("claude", "DONE task-1"))
	if !ok || from != "claude" || text != "DONE task-1" {
		t.Fatalf("got from=%q text=%q ok=%v", from, text, ok)
	}
}

func TestMessageWithBracketsInText(t *testing.T) {
	from, text, ok := ParseMessage(Message("a", "x [b] y"))
	if !ok || from != "a" || text != "x [b] y" {
		t.Fatalf("got from=%q text=%q ok=%v", from, text, ok)
	}
}

func TestValidName(t *testing.T) {
	good := []string{"codex-1", "claude_laptop", "hub", "a.b.c", "A1"}
	for _, n := range good {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false, want true", n)
		}
	}
	// Shell metacharacters, spaces, path traversal, brackets, over-length.
	bad := []string{"", "a b", "x;rm -rf ~", "a$(id)", "../etc", "a/b", "[a]", "a|b", "a&b", "a`b`", strings_repeat("a", 65)}
	for _, n := range bad {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true, want false (injection/oversize risk)", n)
		}
	}
}

func strings_repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func TestAddressedRoundTrip(t *testing.T) {
	line := Addressed("bob", `A2A-MSG {"messageId":"m1"}`)
	to, payload, ok := ParseAddressed(line)
	if !ok {
		t.Fatalf("ParseAddressed rejected %q", line)
	}
	if to != "bob" || payload != `A2A-MSG {"messageId":"m1"}` {
		t.Fatalf("got to=%q payload=%q", to, payload)
	}
}

func TestParseAddressedRejectsPlainLines(t *testing.T) {
	for _, line := range []string{"hi there", "[a] hi", "* notice", "TO", "TO onlyname", ""} {
		if _, _, ok := ParseAddressed(line); ok {
			t.Fatalf("ParseAddressed accepted %q", line)
		}
	}
}

func TestNotice(t *testing.T) {
	if !IsNotice(Notice("codex-1 hopped on the bus")) {
		t.Fatal("Notice output not recognized by IsNotice")
	}
	if IsNotice("[a] hi") {
		t.Fatal("message misidentified as notice")
	}
}
