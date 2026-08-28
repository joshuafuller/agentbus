package bus

import (
	"testing"
	"time"
)

// The WAN finding: a flat 5s write deadline killed 171KB blob-chunk
// lines over a throttled DERP relay. The allowance scales with size.
func TestWriteAllowanceScalesWithLineSize(t *testing.T) {
	if got := writeAllowance(0); got != 5*time.Second {
		t.Fatalf("empty line allowance %v, want 5s", got)
	}
	small := writeAllowance(200)
	if small < 5*time.Second || small > 6*time.Second {
		t.Fatalf("chat-line allowance %v, want ~5s", small)
	}
	big := writeAllowance(171 << 10) // a 128KB-raw chunk line, base64
	if big < 25*time.Second {
		t.Fatalf("171KB line allowance %v — the flat-5s bug again", big)
	}
}
