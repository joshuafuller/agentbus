package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tailscale/tailcat"
)

// Host state (#34): the ticket encodes the host's node key and DERP
// region — nothing else. Persisting that identity under the state dir
// means a restarted host resumes the SAME ticket: riders reconnect on
// their own retry, issued boarding passes stay valid, and the durable
// spool bridges the gap. Rotation (the ticket is the bus password)
// becomes an explicit act: host --new-ticket.

const (
	hostIdentityFile = "identity.json"
	hostTOFUFile     = "tofu.json"
)

// hostStateDir is where a host keeps its restart-surviving state:
// identity (the ticket) and TOFU bindings (the trust table).
func hostStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agentbus", "host"), nil
}

// loadHostIdentity returns the persisted host identity, or (nil, nil)
// when none exists — a fresh host, not an error. A corrupt identity is
// an error: silently minting a new ticket would strand every rider
// without anyone deciding that.
func loadHostIdentity(dir string) (*tailcat.PrivateKey, error) {
	data, err := os.ReadFile(filepath.Join(dir, hostIdentityFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pk := &tailcat.PrivateKey{}
	if err := json.Unmarshal(data, pk); err != nil {
		return nil, fmt.Errorf("corrupt host identity at %s: %w (use --new-ticket to rotate deliberately)", filepath.Join(dir, hostIdentityFile), err)
	}
	if pk.Private.IsZero() {
		return nil, fmt.Errorf("corrupt host identity at %s: empty key (use --new-ticket to rotate deliberately)", filepath.Join(dir, hostIdentityFile))
	}
	return pk, nil
}

// saveHostIdentity writes the identity (0600, atomic) under dir (0700).
// The file holds the node PRIVATE key: whoever reads it can impersonate
// the bus.
func saveHostIdentity(dir string, pk *tailcat.PrivateKey) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(pk, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+hostIdentityFile+".tmp*")
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
	return os.Rename(tmp.Name(), filepath.Join(dir, hostIdentityFile))
}

// resetHostState is --new-ticket: wipe the identity AND the TOFU
// bindings together. A new ticket with old bindings would refuse every
// rider that re-boards with a fresh key; old trust does not belong to
// a new bus.
func resetHostState(dir string) error {
	for _, f := range []string{hostIdentityFile, hostTOFUFile} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
