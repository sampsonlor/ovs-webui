//go:build !linux

package tlscontrol

import "time"

func Now() Clock { return Clock{Wall: time.Now()} }
