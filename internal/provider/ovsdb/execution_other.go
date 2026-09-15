//go:build !linux

package ovsdb

import "github.com/sampsonlor/ovs-webui/internal/inventory"

func sameExecutionFile(Options, int, inventory.Evidence) bool { return false }
