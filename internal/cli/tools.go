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
	if *jsonOut {
		return encode(stdout, agent.Tools())
	}
	for _, tool := range agent.Tools() {
		fmt.Fprintf(stdout, "%s\t%s\n", tool.Name, tool.Description)
	}
	return nil
}
