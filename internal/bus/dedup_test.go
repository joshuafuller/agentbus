package bus

import (
	"fmt"
	"testing"
)

func TestDedupSeenOnceOnly(t *testing.T) {
	d := NewDedup(4)
	if d.Seen("a") {
		t.Fatal("fresh id reported seen")
	}
	if !d.Seen("a") {
		t.Fatal("repeat id not reported seen")
	}
}

func TestDedupSurvivesNonPositiveCapacity(t *testing.T) {
	// A zero/negative capacity must not panic Seen. (PR #18 review.)
	for _, n := range []int{0, -3} {
		d := NewDedup(n)
		d.Seen("a")
		d.Seen("b") // would panic on evict-from-empty before the guard
	}
}

func TestDedupEvictsOldestBeyondCapacity(t *testing.T) {
	d := NewDedup(3)
	for i := 0; i < 4; i++ {
		d.Seen(fmt.Sprint(i)) // 0 evicted when 3 arrives
	}
	if d.Seen("0") == true {
		t.Fatal("evicted id still reported seen")
	}
	if !d.Seen("3") {
		t.Fatal("recent id lost")
	}
}
