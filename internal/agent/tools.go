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
			Name:        "save_connection",
			Description: "Save a named Postgres or MySQL connection in the private configuration; output never includes the DSN.",
			Command:     []string{"dbbie", "connect", "$connection", "$driver", "$dsn", "--json"},
			Inputs:      map[string]string{"connection": "saved connection name", "driver": "postgres|mysql", "dsn": "secret connection string"},
			Safety:      "writes private local configuration only; redacts credentials",
		},
		{
			Name:        "list_connections",
			Description: "List saved connection names and drivers without exposing DSNs.",
			Command:     []string{"dbbie", "connections", "--json"},
			Inputs:      map[string]string{},
			Safety:      "redacts credentials",
		},
		{
			Name:        "list_tables",
			Description: "List database tables with estimatedRows (-1 means unknown); --match filters name or schema.name using a glob.",
			Command:     []string{"dbbie", "tables", "--conn", "$connection", "--json"},
			Inputs:      map[string]string{"connection": "saved connection name (or use --driver and --dsn)", "match": "optional --match GLOB on name or schema.name"},
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
			Description: "Run read-only SQL with timeout and bounded rows capped at 1000. --why records intent, --title labels the run, --no-log (or DBBIE_NO_LOG=1) disables logging. Returns a per-connection log id; failures are logged too. Actor is DBBIE_ACTOR, else agent with --why or non-terminal stdout, else human.",
			Command:     []string{"dbbie", "query", "--conn", "$connection", "--sql", "$sql", "--why", "$why", "--limit", "$limit", "--json"},
			Inputs:      map[string]string{"connection": "saved connection name (or use --driver and --dsn; log name adhoc)", "sql": "read-only SQL", "limit": "preview row limit, max 1000", "why": "--why TEXT: reason for the query", "title": "optional --title TEXT", "timeout": "optional --timeout duration, default 10s", "no-log": "optional --no-log; retains legacy hash id", "allow-write": "optional --allow-write explicitly disables read-only protection"},
			Safety:      "read-only by default; bounded preview; rejects multi-statement SQL",
		},
		{
			Name:        "list_query_log",
			Description: "List recent stored query summaries, newest last, without rows. Includes id, ts, actor, title, why, sql, rowCount, error.",
			Command:     []string{"dbbie", "log", "--conn", "$connection", "--limit", "$limit", "--json"},
			Inputs:      map[string]string{"connection": "connection log name; adhoc for raw DSN queries", "limit": "recent entries, default 20; <=0 means all"},
			Safety:      "local log only; never executes SQL or exposes a DSN",
		},
		{
			Name:        "show_query",
			Description: "Read a stored query entry and page its preview rows with --offset and --limit. rowCount stays the original total; SQL is never re-run.",
			Command:     []string{"dbbie", "show", "$id", "--conn", "$connection", "--offset", "$offset", "--limit", "$limit", "--json"},
			Inputs:      map[string]string{"id": "q_ log id", "connection": "connection log name", "offset": "non-negative row offset, default 0", "limit": "non-negative page size, default 100, capped at 1000"},
			Safety:      "stored bounded preview only; never executes SQL",
		},
		{
			Name:        "open_workspace",
			Description: "Open the interactive TUI at a database workspace.",
			Command:     []string{"dbbie", "open", "--conn", "$connection", "--theme", "$theme"},
			Inputs:      map[string]string{"connection": "saved connection name (or use --driver and --dsn)", "theme": "midnight|light|contrast", "sql": "optional --sql initial read-only query", "limit": "optional --limit preview size, max 1000"},
			Safety:      "human-in-the-loop TUI",
		},
		{
			Name:        "list_tools",
			Description: "Describe all CLI commands. JSON defaults on for non-terminal stdout; --json=false opts into human output. JSON errors are {error:{code,message}} on stdout with a non-zero exit.",
			Command:     []string{"dbbie", "tools", "--json"},
			Inputs:      map[string]string{},
			Safety:      "static manifest only",
		},
		{
			Name:        "help",
			Description: "Print CLI usage and safety defaults.",
			Command:     []string{"dbbie", "--help"},
			Inputs:      map[string]string{},
			Safety:      "static help only",
		},
		{
			Name:        "serve_mcp",
			Description: "Reserved MCP server command; currently returns a not-implemented error. Use the JSON CLI instead.",
			Command:     []string{"dbbie", "serve-mcp"},
			Inputs:      map[string]string{},
			Safety:      "not implemented; starts no server",
		},
	}
}
