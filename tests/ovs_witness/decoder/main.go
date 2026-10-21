// Isolated CI codec probe. This is not shipped as a daemon or command surface.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/ovswitness"
)

func main() {
	nonce := flag.String("nonce", "", "expected per-request nonce")
	bridge := flag.String("bridge", "", "expected immutable OVS UUID")
	input := flag.Bool("input", false, "hash an independent native input image")
	exclusive := flag.Bool("exclusive", false, "check point-in-time protocol exclusivity, not execution authority")
	flag.Parse()
	limit := ovswitness.MaxWireBytes
	if *input {
		limit = 1 << 20
	}
	wire, err := io.ReadAll(io.LimitReader(os.Stdin, int64(limit)+1))
	if err == nil && *input && len(wire) <= limit {
		var rows map[string][]map[string]any
		d := json.NewDecoder(strings.NewReader(string(wire)))
		d.UseNumber()
		err = d.Decode(&rows)
		if err == nil {
			var hash string
			hash, err = ovswitness.Digest(rows)
			if err == nil {
				fmt.Println(hash)
				return
			}
		}
	} else if err == nil {
		var sample ovswitness.Sample
		sample, err = ovswitness.Decode(wire, *nonce, *bridge)
		if err == nil && *exclusive {
			err = ovswitness.Exclusive(sample)
		}
		if err == nil {
			_ = json.NewEncoder(os.Stdout).Encode(sample)
			return
		}
	}
	fmt.Fprintln(os.Stderr, "SPANNING_TREE_WITNESS_REJECTED")
	os.Exit(1)
}
