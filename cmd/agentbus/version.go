package main

import (
	"fmt"
	"io"
)

var version, commit, date = "dev", "unknown", "unknown"

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "agentbus %s (%s, %s)\n", version, commit, date)
}
