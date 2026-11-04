package cli

import (
	"fmt"
	"io"
	"runtime/debug"
)

// Release builds set these with -ldflags "-X github.com/yeshwanthyk/dbbie/internal/cli.version=...".
var (
	version = ""
	commit  = ""
	date    = ""
)

// versionString reports the release version, falling back to the module
// version `go install …@vX` records and then to the VCS revision of a local build.
func versionString() string {
	v, c, d := version, commit, date
	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
		for _, s := range info.Settings {
			switch {
			case s.Key == "vcs.revision" && c == "":
				c = s.Value
			case s.Key == "vcs.time" && d == "":
				d = s.Value
			}
		}
	}
	if v == "" {
		v = "dev"
	}
	if len(c) > 12 {
		c = c[:12]
	}
	out := "dbbie " + v
	if c != "" {
		out += " (" + c
		if d != "" {
			out += ", " + d
		}
		out += ")"
	}
	return out
}

func printVersion(w io.Writer) error {
	_, err := fmt.Fprintln(w, versionString())
	return err
}
