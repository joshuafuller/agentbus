package bus

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

const testPoll = 10 * time.Millisecond

func TestAwaitReturnsPendingImmediately(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	os.WriteFile(inbox, []byte("[a] one\n[b] two\n"), 0o644)

	start := time.Now()
	lines, err := Await(inbox, testPoll)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("Await blocked on already-pending lines")
	}
	if want := []string{"[a] one", "[b] two"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("got %v, want %v", lines, want)
	}
}

func TestAwaitTracksOffsetAcrossCalls(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	os.WriteFile(inbox, []byte("[a] one\n"), 0o644)
	if _, err := Await(inbox, testPoll); err != nil {
		t.Fatal(err)
	}

	f, _ := os.OpenFile(inbox, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("[a] two\n")
	f.Close()

	lines, err := Await(inbox, testPoll)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"[a] two"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("second call got %v, want %v", lines, want)
	}
}

func TestAwaitBlocksUntilAppend(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	go func() {
		time.Sleep(50 * time.Millisecond)
		os.WriteFile(inbox, []byte("[x] late\n"), 0o644)
	}()
	lines, err := Await(inbox, testPoll)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"[x] late"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("got %v, want %v", lines, want)
	}
}

func TestAwaitIgnoresPartialLine(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	os.WriteFile(inbox, []byte("[a] done\n[b] not yet"), 0o644)
	lines, err := Await(inbox, testPoll)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"[a] done"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("got %v, want %v", lines, want)
	}
}

func TestAwaitHandlesTruncation(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	os.WriteFile(inbox, []byte("[a] one\n[a] two\n"), 0o644)
	if _, err := Await(inbox, testPoll); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(inbox, []byte("[c] fresh\n"), 0o644) // truncated shorter
	lines, err := Await(inbox, testPoll)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"[c] fresh"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("got %v, want %v", lines, want)
	}
}
