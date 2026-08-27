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

func TestNotice(t *testing.T) {
	if !IsNotice(Notice("codex-1 hopped on the bus")) {
		t.Fatal("Notice output not recognized by IsNotice")
	}
	if IsNotice("[a] hi") {
		t.Fatal("message misidentified as notice")
	}
}
