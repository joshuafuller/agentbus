package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeHelperPrivateIPCAndParentOwnership(t *testing.T) {
	for _, crash := range []bool{false, true} {
		ep, _, err := startNative(t.Context(), nativeConfig{DirectOnly: true}, nativeOptions{BindAddr: "127.0.0.1:0"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ep.Shutdown(context.Background()) })
		for path, mode := range map[string]os.FileMode{ep.dir: 0700, filepath.Join(ep.dir, "stream.sock"): 0600} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != mode {
				t.Fatalf("private IPC mode: %v", err)
			}
		}
		if len(ep.cmd.Args) != 1 {
			t.Fatal("helper configuration leaked into argv")
		}
		if crash {
			if err := ep.cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
		} else {
			ep.stdin.Close()
		}
		select {
		case <-ep.Closed():
		case <-time.After(3 * time.Second):
			t.Fatal("helper survived parent EOF or process crash")
		}
		if c, err := ep.listener.Accept(); err == nil {
			c.Close()
			t.Fatal("dead helper retained its listener")
		}
		if !crash {
			if _, err := os.Stat(ep.dir); !os.IsNotExist(err) {
				t.Fatal("parent EOF leaked private IPC directory")
			}
		}
		ep.Shutdown(context.Background())
		if _, err := os.Stat(ep.dir); !os.IsNotExist(err) {
			t.Fatal("private IPC directory leaked")
		}
	}
}
