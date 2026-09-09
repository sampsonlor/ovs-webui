package buildinfo

import "runtime/debug"

// Release builds may set these with -ldflags. Both daemons use the same identity.
var Version = "0.1.0-dev"
var Commit = "unknown"

func SoftwareVersion() string {
	revision := Commit
	dirty := ""
	if revision == "unknown" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				switch setting.Key {
				case "vcs.revision":
					revision = setting.Value
				case "vcs.modified":
					if setting.Value == "true" {
						dirty = ".dirty"
					}
				}
			}
		}
	}
	return Version + "+" + revision + dirty
}
