package bus

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Rider identity (issue #6, ADR 0002): every rider owns an Ed25519
// keypair; the hub binds a name to the first key that claims it (TOFU)
// and refuses later connections under that name that cannot prove the
// same key. Proof is a signature over a FRESH hub nonce plus the name
// — a static signed artifact cannot be replayed on a later join
// (PR #4 review). The ticket admits; the key identifies.

// keyFile is the rider's private key, raw ed25519 seed, base64, 0600.
const keyFile = "id_ed25519"

// LoadOrCreateKey returns the rider key stored in dir, creating and
// persisting one (0600) on first use.
func LoadOrCreateKey(dir string) (ed25519.PrivateKey, error) {
	path := filepath.Join(dir, keyFile)
	if data, err := os.ReadFile(path); err == nil {
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("corrupt rider key at %s", path)
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	// Exclusive creation: two concurrent first-joins must converge on
	// ONE key — if the loser overwrote the file after the hub bound the
	// winner's key, the name would be unclaimable on the next reconnect
	// for the bus's lifetime (PR #20 review). The loser loads the
	// winner's key instead.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return LoadOrCreateKey(dir)
	}
	if err != nil {
		return nil, err
	}
	enc := base64.StdEncoding.EncodeToString(priv.Seed())
	if _, err := f.WriteString(enc + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return priv, nil
}

// challengeTranscript is what a joiner signs: the hub's fresh nonce
// bound to the claimed name, domain-separated so these signatures can
// never be confused with any future signing use of the same key.
func challengeTranscript(nonce, name string) []byte {
	return []byte("agentbus-join-v1\x00" + nonce + "\x00" + name)
}

// SignChallenge signs the join transcript with the rider key.
func SignChallenge(key ed25519.PrivateKey, nonce, name string) []byte {
	return ed25519.Sign(key, challengeTranscript(nonce, name))
}

// VerifyChallenge reports whether sig proves possession of the key
// bound to name, for this connection's nonce.
func VerifyChallenge(pub ed25519.PublicKey, nonce, name string, sig []byte) bool {
	return ed25519.Verify(pub, challengeTranscript(nonce, name), sig)
}

// HelloKeyed formats a greeting that carries the joiner's public key.
func HelloKeyed(name string, oneshot bool, pub ed25519.PublicKey) string {
	line := "HELLO " + name
	if oneshot {
		line += " oneshot"
	}
	return line + " key=" + base64.StdEncoding.EncodeToString(pub)
}

// ParseHelloKeyed extracts name, mode, and (optionally) the public key
// from any greeting line. pub is nil for a legacy unkeyed hello.
func ParseHelloKeyed(line string) (name string, oneshot bool, pub ed25519.PublicKey, ok bool) {
	rest, found := strings.CutPrefix(line, "HELLO ")
	if !found {
		return "", false, nil, false
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", false, nil, false
	}
	name = fields[0]
	for _, f := range fields[1:] {
		switch {
		case f == "oneshot":
			oneshot = true
		case strings.HasPrefix(f, "key="):
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(f, "key="))
			if err != nil || len(raw) != ed25519.PublicKeySize {
				return "", false, nil, false
			}
			pub = ed25519.PublicKey(raw)
		default:
			return "", false, nil, false
		}
	}
	// Full ValidName enforcement at the wire: names reach file paths
	// (spool, rider dirs) and shell env; a direct-protocol speaker must
	// not get past what the CLI enforces (PR #20 review).
	if !ValidName(name) {
		return "", false, nil, false
	}
	return name, oneshot, pub, true
}

// Challenge formats the hub's fresh-nonce challenge (hub → joiner,
// before the welcome).
func Challenge(nonce string) string {
	return "CHAL " + nonce
}

// ParseChallenge extracts the nonce from a challenge line.
func ParseChallenge(line string) (nonce string, ok bool) {
	rest, found := strings.CutPrefix(line, "CHAL ")
	if !found || strings.TrimSpace(rest) == "" {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// SigLine formats the joiner's signature reply (joiner → hub).
func SigLine(sig []byte) string {
	return "SIG " + base64.StdEncoding.EncodeToString(sig)
}

// ParseSig extracts the signature from a reply line.
func ParseSig(line string) (sig []byte, ok bool) {
	rest, found := strings.CutPrefix(line, "SIG ")
	if !found {
		return nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rest))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return nil, false
	}
	return raw, true
}

// NewNonce returns a fresh random challenge nonce.
func NewNonce() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// PubOf is the typed public half of a rider key.
func PubOf(k ed25519.PrivateKey) ed25519.PublicKey {
	return k.Public().(ed25519.PublicKey)
}

// LoadKeyIfExists returns the rider key stored in dir, or nil if none
// exists — senders authenticate only when they hold the name's key.
func LoadKeyIfExists(dir string) (ed25519.PrivateKey, error) {
	if _, err := os.Stat(filepath.Join(dir, keyFile)); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		// A permission or IO error must surface — silently degrading to
		// unauthenticated invites confusing refusals later (PR #20).
		return nil, err
	}
	return LoadOrCreateKey(dir)
}

// ClientHello performs the client half of the greeting: it writes the
// (keyed) hello and, when a key is presented, answers the hub's fresh
// challenge. sc must be the caller's own scanner over conn — the
// welcome (or a refusal notice) is the next line it will read.
func ClientHello(conn io.Writer, sc *bufio.Scanner, name string, oneshot bool, key ed25519.PrivateKey) error {
	if key == nil {
		if oneshot {
			_, err := fmt.Fprintf(conn, "%s\n", HelloOneshot(name))
			return err
		}
		_, err := fmt.Fprintf(conn, "%s\n", Hello(name))
		return err
	}
	if _, err := fmt.Fprintf(conn, "%s\n", HelloKeyed(name, oneshot, PubOf(key))); err != nil {
		return err
	}
	if !sc.Scan() {
		return fmt.Errorf("bus closed during the identity handshake")
	}
	nonce, ok := ParseChallenge(sc.Text())
	if !ok {
		return fmt.Errorf("expected an identity challenge, got %q", sc.Text())
	}
	_, err := fmt.Fprintf(conn, "%s\n", SigLine(SignChallenge(key, nonce, name)))
	return err
}
