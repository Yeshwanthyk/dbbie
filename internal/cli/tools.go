package cli

import (
	"fmt"
	"io"

	"github.com/yeshwanthyk/dbbie/internal/agent"
)

func tools(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("tools")
	jsonOut := fs.Bool("json", !isTerminal(stdout), "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	manifest := agent.Tools()
	for i := range manifest {
		if manifest[i].Name == "open_workspace" {
			manifest[i].Inputs["session"] = "optional --session NAME; opens stored result tabs without re-running SQL; uses the session connection"
			manifest[i].Inputs["connection"] = "optional saved connection name; omitted opens a fuzzy picker (one saved connection opens directly)"
			manifest[i].Description = "Open the interactive TUI; --session restores stored result tabs, r re-runs read-only and logged, :save/:add manage sessions, :sessions/:conn open fuzzy pickers."
		}
	}
	if *jsonOut {
		return encode(stdout, manifest)
	}
	for _, tool := range manifest {
		fmt.Fprintf(stdout, "%s\t%s\n", tool.Name, tool.Description)
	}
	return nil
}
