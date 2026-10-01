package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/go-iroh/key"
)

// Host state (#34): the ticket carries the Iroh endpoint address and
// a separate admission secret. Persisting that identity under the state dir
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
func loadHostIdentity(dir string) (*hostIdentity, error) {
	data, err := os.ReadFile(filepath.Join(dir, hostIdentityFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pk := &hostIdentity{}
	if err := json.Unmarshal(data, pk); err != nil {
		return nil, fmt.Errorf("corrupt host identity at %s: %w (use --new-ticket to rotate deliberately)", filepath.Join(dir, hostIdentityFile), err)
	}
	if pk.Version != 1 {
		return nil, fmt.Errorf("legacy or unsupported host identity at %s: Iroh requires host --new-ticket and new boarding passes", filepath.Join(dir, hostIdentityFile))
	}
	if _, err := key.SecretKeyFromSlice(pk.Private); err != nil || len(pk.Admission) != admissionBytes || subtle.ConstantTimeCompare(pk.Admission, make([]byte, admissionBytes)) == 1 || pk.Relay == "" {
		return nil, fmt.Errorf("corrupt host identity at %s: missing key, admission secret or relay (use --new-ticket to rotate deliberately)", filepath.Join(dir, hostIdentityFile))
	}
	if _, err := parseRelay(pk.Relay); err != nil {
		return nil, fmt.Errorf("corrupt host relay (use --new-ticket to rotate deliberately)")
	}
	return pk, nil
}

// saveHostIdentity writes the identity (0600, atomic) under dir (0700).
// The file holds the node PRIVATE key and admission secret: whoever reads it can impersonate
// the bus.
func saveHostIdentity(dir string, pk *hostIdentity) error {
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
