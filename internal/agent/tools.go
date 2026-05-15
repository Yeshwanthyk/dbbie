package agent

type Tool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Command     []string          `json:"command"`
	Inputs      map[string]string `json:"inputs"`
	Safety      string            `json:"safety"`
}

func Tools() []Tool {
	return []Tool{
		{
			Name:        "list_connections",
			Description: "List saved connection names and drivers without exposing DSNs.",
			Command:     []string{"dbbie", "connections", "--json"},
			Inputs:      map[string]string{},
			Safety:      "redacts credentials",
		},
		{
			Name:        "list_tables",
			Description: "List database tables for a supplied driver and DSN.",
			Command:     []string{"dbbie", "tables", "--conn", "$connection", "--json"},
			Inputs:      map[string]string{"connection": "saved connection name"},
			Safety:      "catalog-only",
		},
		{
			Name:        "describe_table",
			Description: "Describe table columns. Schema is optional for public Postgres tables and current MySQL database.",
			Command:     []string{"dbbie", "describe", "--conn", "$connection", "--schema", "$schema", "--table", "$table", "--json"},
			Inputs:      map[string]string{"connection": "saved connection name", "schema": "optional schema/database", "table": "table name"},
			Safety:      "catalog-only",
		},
		{
			Name:        "run_readonly_query",
			Description: "Run read-only SQL with timeout, server-side preview wrapping for SELECT/WITH, and bounded JSON rows capped at 1000.",
			Command:     []string{"dbbie", "query", "--conn", "$connection", "--sql", "$sql", "--limit", "$limit", "--json"},
			Inputs:      map[string]string{"connection": "saved connection name", "sql": "read-only SQL", "limit": "preview row limit, max 1000"},
			Safety:      "read-only by default; bounded preview; rejects multi-statement SQL",
		},
		{
			Name:        "open_workspace",
			Description: "Open the interactive TUI at a database workspace.",
			Command:     []string{"dbbie", "open", "--conn", "$connection", "--theme", "$theme"},
			Inputs:      map[string]string{"connection": "saved connection name", "theme": "midnight|light|contrast"},
			Safety:      "human-in-the-loop TUI",
		},
	}
}
