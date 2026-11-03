package agent

import (
	"strings"
	"testing"
)

func TestToolsExposeAIFirstContracts(t *testing.T) {
	tools := Tools()
	if len(tools) < 4 {
		t.Fatalf("tools len = %d, want at least 4", len(tools))
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		if tool.Name == "" || len(tool.Command) == 0 || tool.Safety == "" {
			t.Fatalf("incomplete tool: %+v", tool)
		}
		seen[tool.Name] = true
	}
	for _, name := range []string{"save_connection", "list_connections", "list_tables", "describe_table", "run_readonly_query", "list_query_log", "show_query", "open_workspace", "list_tools", "help", "serve_mcp"} {
		if !seen[name] {
			t.Fatalf("missing tool %s", name)
		}
	}
}

func TestManifestDescribesEveryCommandAndNewFlags(t *testing.T) {
	commands := map[string]bool{}
	for _, tool := range Tools() {
		if len(tool.Command) < 2 || tool.Command[0] != "dbbie" {
			t.Fatalf("invalid command: %+v", tool)
		}
		commands[tool.Command[1]] = true
		if tool.Name == "run_readonly_query" {
			for _, flag := range []string{"--why", "--title", "--no-log"} {
				if !strings.Contains(tool.Description, flag) {
					t.Fatalf("query manifest missing %s", flag)
				}
			}
			if _, ok := tool.Inputs["why"]; !ok {
				t.Fatal("missing why input")
			}
		}
		if tool.Name == "list_tables" && !strings.Contains(tool.Description, "--match") {
			t.Fatal("missing --match")
		}
		if tool.Name == "show_query" && !strings.Contains(tool.Description, "never re-run") {
			t.Fatal("show safety not described")
		}
	}
	for _, command := range []string{"connect", "connections", "tables", "describe", "query", "log", "show", "open", "tools", "--help", "serve-mcp"} {
		if !commands[command] {
			t.Fatalf("command %s missing from manifest", command)
		}
	}
}
