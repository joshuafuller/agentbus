package bus

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// TOFU persistence (#34): with a persistent ticket a host restart is
// an in-place upgrade, not a trust reset. A hub that forgot its
// bindings across a restart would re-open the trust-on-first-use
// window for every known rider name — an attacker could claim one
// before its owner reconnects. The valve for a deliberate reset is
// host --new-ticket, which wipes this file with the identity.

// PersistBindings loads the TOFU table stored at path (if it exists)
// and arms saving: every future bind rewrites the file (0600, atomic).
// A corrupt file is an error — silently starting with an empty trust
// table would be a downgrade attack surface, not a recovery.
func (h *Hub) PersistBindings(path string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	loaded := make(map[string]ed25519.PublicKey)
	if err == nil {
		var raw map[string]string
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("corrupt TOFU bindings at %s: %w", path, err)
		}
		for name, enc := range raw {
			pub, err := base64.StdEncoding.DecodeString(enc)
			if err != nil || len(pub) != ed25519.PublicKeySize || !ValidName(name) {
				return fmt.Errorf("corrupt TOFU binding for %q at %s", name, path)
			}
			loaded[name] = ed25519.PublicKey(pub)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, pub := range loaded {
		h.bindings[name] = pub
	}
	h.bindingsPath = path
	return nil
}

// saveBindingsLocked rewrites the bindings file. Callers hold h.mu.
// Binds are rare (once per new rider name), so a full rewrite is fine.
// The write is atomic (temp + rename) so a crash mid-write can never
// leave a half-written trust table for the next start to refuse.
func (h *Hub) saveBindingsLocked() error {
	if h.bindingsPath == "" {
		return nil
	}
	raw := make(map[string]string, len(h.bindings))
	for name, pub := range h.bindings {
		raw[name] = base64.StdEncoding.EncodeToString(pub)
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(h.bindingsPath), ".tofu.json.tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), h.bindingsPath)
}
