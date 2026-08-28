package main

import (
	"net"
	"time"
)

// livenessReader extends the connection's read deadline on every
// arriving byte once armed. Refreshing only on COMPLETE lines starves
// large frames: a 174KB blob-chunk line trickling over a slow DERP
// path can take longer than the whole liveness window to finish, so a
// per-line refresh killed healthy transfers mid-line and the rider
// looped through disconnect/reconnect forever (WAN test finding).
// Bytes flowing IS liveness; line completion is not.
//
// Not safe for concurrent use — the scanner and the session loop that
// arms it run on one goroutine.
type livenessReader struct {
	conn   net.Conn
	window time.Duration // 0 = not armed (legacy no-PONG host)
}

func (r *livenessReader) Read(p []byte) (int, error) {
	n, err := r.conn.Read(p)
	if n > 0 && r.window > 0 {
		r.conn.SetReadDeadline(time.Now().Add(r.window))
	}
	return n, err
}

// arm starts (or refreshes) the liveness window: from now on, silence
// longer than the window ends the session.
func (r *livenessReader) arm(window time.Duration) {
	r.window = window
	r.conn.SetReadDeadline(time.Now().Add(window))
}

func (r *livenessReader) armed() bool { return r.window > 0 }
