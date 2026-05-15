package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"io"

	"github.com/yeshwanthyk/dbbie/internal/agent"
)

func tools(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tools", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*jsonOut {
		return errors.New("tools is agent-facing; pass --json")
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(agent.Tools())
}
