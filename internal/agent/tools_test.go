package agent

import "testing"

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
	for _, name := range []string{"list_connections", "list_tables", "describe_table", "run_readonly_query", "open_workspace"} {
		if !seen[name] {
			t.Fatalf("missing tool %s", name)
		}
	}
}
