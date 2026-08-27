package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joshuafuller/agentbus/internal/bus"
)

// putChunkBytes is the raw file bytes per BLOB chunk. base64 inflates
// ~4/3, so this stays well under the hub's 256KB line cap.
const putChunkBytes = 128 << 10

// runPut streams one file to a named rider, out of band: the bytes
// travel as BLOB frames the receiver spools content-addressed, and the
// agent sees one FILE line instead of the payload (issue #2). Exit
// codes match task: 0 sent, 2 could not send.
func runPut(ticket, name, rider, path string, timeout time.Duration) error {
	if !bus.ValidName(rider) {
		fmt.Fprintf(os.Stderr, "agentbus: invalid rider name %q\n", rider)
		os.Exit(2)
	}
	conn, err := dial(ticket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentbus: could not reach the bus: %v\n", err)
		os.Exit(2)
	}
	var key ed25519.PrivateKey
	if rdir, rerr := riderDir(name); rerr == nil {
		key, rerr = bus.LoadKeyIfExists(rdir)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "agentbus: rider key: %v\n", rerr)
			os.Exit(2)
		}
	}
	os.Exit(runPutConn(conn, name, rider, path, key, os.Stdout))
	return nil
}

// runPutConn is the transport-independent body, split out so tests can
// drive it over an in-memory hub. It owns closing conn.
func runPutConn(conn net.Conn, name, rider, path string, key ed25519.PrivateKey, out io.Writer) int {
	defer conn.Close()

	if !bus.ValidName(rider) {
		fmt.Fprintf(out, "invalid rider name %q\n", rider)
		return 2
	}
	// The blob name is the basename; it must pass the same wire rule
	// the receiver enforces, or the transfer is refused on arrival.
	base := filepath.Base(path)
	if !bus.ValidName(base) {
		fmt.Fprintf(out, "file name %q is not safe to send (letters, digits, dash, underscore, dot)\n", base)
		return 2
	}
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(out, "cannot read %s: %v\n", path, err)
		return 2
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		fmt.Fprintf(out, "not a regular file: %s\n", path)
		return 2
	}

	conn.SetDeadline(time.Now().Add(5 * time.Minute))
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	if err := bus.ClientHello(conn, sc, name, false, key); err != nil {
		fmt.Fprintf(out, "identity handshake failed: %v\n", err)
		return 2
	}
	if !sc.Scan() || !strings.Contains(sc.Text(), "welcome aboard") {
		fmt.Fprintf(out, "bus refused this connection: %s\n", sc.Text())
		return 2
	}

	// Hash and chunk the file in one streaming pass so a large artifact
	// is never held whole in memory (issue #2 streaming note).
	id, err := bus.NewNonce()
	if err != nil {
		fmt.Fprintf(out, "could not start transfer: %v\n", err)
		return 2
	}
	total := int((info.Size() + putChunkBytes - 1) / putChunkBytes)
	if total == 0 {
		total = 1
	}
	sum, err := fileSum(path)
	if err != nil {
		fmt.Fprintf(out, "could not read %s: %v\n", path, err)
		return 2
	}
	hdr := bus.BlobHeader{ID: id, Name: base, Size: info.Size(), Total: total, Sum: sum}
	if _, err := fmt.Fprintf(conn, "%s\n", bus.Addressed(rider, hdr.Encode())); err != nil {
		fmt.Fprintf(out, "send failed: %v\n", err)
		return 2
	}
	buf := make([]byte, putChunkBytes)
	if info.Size() == 0 {
		if _, err := fmt.Fprintf(conn, "%s\n", bus.Addressed(rider, bus.BlobChunk(id, 1, nil))); err != nil {
			fmt.Fprintf(out, "send failed at chunk 1: %v\n", err)
			return 2
		}
	} else {
		for seq := 1; ; seq++ {
			n, rerr := io.ReadFull(f, buf)
			if n > 0 {
				if _, err := fmt.Fprintf(conn, "%s\n", bus.Addressed(rider, bus.BlobChunk(id, seq, buf[:n]))); err != nil {
					fmt.Fprintf(out, "send failed at chunk %d: %v\n", seq, err)
					return 2
				}
			}
			if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
				break
			}
			if rerr != nil {
				fmt.Fprintf(out, "read failed at chunk %d: %v\n", seq, rerr)
				return 2
			}
		}
	}

	// Block until the receiver confirms the whole blob. A fire-and-
	// forget send races the connection close and silently drops the
	// buffered tail (a 300KB file arrived as a 0-byte partial in
	// testing). The receipt is our delivery proof; withholding the exit
	// until it lands is what makes `put` trustworthy.
	for sc.Scan() {
		from, body, ok := bus.ParseMessage(sc.Text())
		if !ok {
			continue
		}
		payload := body
		if envID, p, isEnv := bus.ParseEnvelope(body); isEnv {
			fmt.Fprintf(conn, "%s\n", bus.Ack(envID))
			payload = p
		}
		rid, delivered, why, isReceipt := bus.ParseBlobReceipt(payload)
		if !isReceipt || rid != id {
			continue // some other traffic addressed to us
		}
		if !delivered {
			fmt.Fprintf(out, "%s rejected %s: %s\n", from, base, why)
			return 2
		}
		fmt.Fprintf(out, "sent %s (%dB) to %s\n", base, info.Size(), rider)
		return 0
	}
	fmt.Fprintf(out, "bus closed before %s confirmed %s\n", rider, base)
	return 2
}

// fileSum streams the sha256 of a file without holding it in memory.
func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
