package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// Save persists a task as dir/<id>.json, written 0600 via a temp file
// and rename so a crash mid-write never leaves a torn task on disk.
// Called on every state transition (ADR 0004: persist per transition).
func Save(dir string, t *a2a.Task) error {
	path, err := taskPath(dir, t.ID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("encoding task %s: %w", t.ID, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads a task back by ID.
func Load(dir string, id a2a.TaskID) (*a2a.Task, error) {
	path, err := taskPath(dir, id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t a2a.Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("decoding task %s: %w", id, err)
	}
	return &t, nil
}

// taskPath maps a task ID to its file, refusing IDs that would escape
// dir. Task IDs are server-generated UUIDs, but the check holds even if
// a hostile peer supplies one.
func taskPath(dir string, id a2a.TaskID) (string, error) {
	name := string(id) + ".json"
	if filepath.Base(name) != name || id == "" {
		return "", fmt.Errorf("invalid task ID %q", id)
	}
	return filepath.Join(dir, name), nil
}
