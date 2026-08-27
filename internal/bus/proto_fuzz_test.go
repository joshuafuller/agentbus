package bus

import (
	"bytes"
	"crypto/ed25519"
	"strings"
	"testing"
)

func FuzzParseHello(f *testing.F) {
	valid := Hello("alice")
	f.Add(valid)
	f.Add(HelloOneshot("alice"))
	f.Add("")
	f.Add(strings.Repeat("x", 1<<20))
	f.Add("\x00garbage")
	f.Add("H")
	f.Add("HELLO")
	f.Add("HELLO ")

	name, oneshot, ok := ParseHello(valid)
	if !ok || name != "alice" || oneshot {
		f.Fatalf("ParseHello(%q) = %q, oneshot=%v, ok=%v", valid, name, oneshot, ok)
	}
	f.Fuzz(func(t *testing.T, line string) {
		ParseHello(line)
	})
}

func FuzzParseHelloKeyed(f *testing.F) {
	pub := ed25519.PublicKey(bytes.Repeat([]byte{0x42}, ed25519.PublicKeySize))
	valid := HelloKeyed("alice", true, pub)
	f.Add(valid)
	f.Add(Hello("alice"))
	f.Add("")
	f.Add(strings.Repeat("x", 1<<20))
	f.Add("\x00garbage")
	f.Add("H")
	f.Add("HELLO")
	f.Add("HELLO ")

	name, oneshot, gotPub, ok := ParseHelloKeyed(valid)
	if !ok || name != "alice" || !oneshot || !bytes.Equal(gotPub, pub) {
		f.Fatalf("ParseHelloKeyed(%q) = %q, oneshot=%v, pub=%x, ok=%v", valid, name, oneshot, gotPub, ok)
	}
	f.Fuzz(func(t *testing.T, line string) {
		ParseHelloKeyed(line)
	})
}

func FuzzParseAddressed(f *testing.F) {
	valid := Addressed("bob", "payload")
	f.Add(valid)
	f.Add("")
	f.Add(strings.Repeat("x", 1<<20))
	f.Add("\x00garbage")
	f.Add("T")
	f.Add("TO")
	f.Add("TO ")

	to, payload, ok := ParseAddressed(valid)
	if !ok || to != "bob" || payload != "payload" {
		f.Fatalf("ParseAddressed(%q) = %q, %q, ok=%v", valid, to, payload, ok)
	}
	f.Fuzz(func(t *testing.T, line string) {
		ParseAddressed(line)
	})
}

func FuzzParseEnvelope(f *testing.F) {
	valid := Envelope("id-1", "payload")
	f.Add(valid)
	f.Add("")
	f.Add(strings.Repeat("x", 1<<20))
	f.Add("\x00garbage")
	f.Add("E")
	f.Add("E:")
	f.Add("E:id-1")

	id, payload, ok := ParseEnvelope(valid)
	if !ok || id != "id-1" || payload != "payload" {
		f.Fatalf("ParseEnvelope(%q) = %q, %q, ok=%v", valid, id, payload, ok)
	}
	f.Fuzz(func(t *testing.T, body string) {
		ParseEnvelope(body)
	})
}

func FuzzParseAck(f *testing.F) {
	valid := Ack("id-1")
	f.Add(valid)
	f.Add("")
	f.Add(strings.Repeat("x", 1<<20))
	f.Add("\x00garbage")
	f.Add("A")
	f.Add("ACKD")
	f.Add("ACKD ")

	id, ok := ParseAck(valid)
	if !ok || id != "id-1" {
		f.Fatalf("ParseAck(%q) = %q, ok=%v", valid, id, ok)
	}
	f.Fuzz(func(t *testing.T, line string) {
		ParseAck(line)
	})
}

func FuzzParseMessage(f *testing.F) {
	valid := Message("alice", "payload")
	f.Add(valid)
	f.Add("")
	f.Add(strings.Repeat("x", 1<<20))
	f.Add("\x00garbage")
	f.Add("[")
	f.Add("[]")
	f.Add("[alice]")

	from, text, ok := ParseMessage(valid)
	if !ok || from != "alice" || text != "payload" {
		f.Fatalf("ParseMessage(%q) = %q, %q, ok=%v", valid, from, text, ok)
	}
	f.Fuzz(func(t *testing.T, line string) {
		ParseMessage(line)
	})
}

func FuzzParseChallenge(f *testing.F) {
	valid := Challenge("nonce-1")
	f.Add(valid)
	f.Add("")
	f.Add(strings.Repeat("x", 1<<20))
	f.Add("\x00garbage")
	f.Add("C")
	f.Add("CHAL")
	f.Add("CHAL ")

	nonce, ok := ParseChallenge(valid)
	if !ok || nonce != "nonce-1" {
		f.Fatalf("ParseChallenge(%q) = %q, ok=%v", valid, nonce, ok)
	}
	f.Fuzz(func(t *testing.T, line string) {
		ParseChallenge(line)
	})
}

func FuzzParseSig(f *testing.F) {
	signature := bytes.Repeat([]byte{0xab}, ed25519.SignatureSize)
	valid := SigLine(signature)
	f.Add(valid)
	f.Add("")
	f.Add(strings.Repeat("x", 1<<20))
	f.Add("\x00garbage")
	f.Add("S")
	f.Add("SIG")
	f.Add("SIG ")

	sig, ok := ParseSig(valid)
	if !ok || !bytes.Equal(sig, signature) {
		f.Fatalf("ParseSig(%q) = %x, ok=%v", valid, sig, ok)
	}
	f.Fuzz(func(t *testing.T, line string) {
		ParseSig(line)
	})
}
