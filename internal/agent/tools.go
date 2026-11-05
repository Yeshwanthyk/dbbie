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
			Name:        "remove_connection",
			Description: "Remove a saved connection from private configuration; logs and sessions are retained.",
			Command:     []string{"dbbie", "connect", "--rm", "$connection", "--json"},
			Inputs:      map[string]string{"connection": "saved connection name"},
			Safety:      "local configuration only; never opens the database or exposes DSNs",
		},
		{
			Name:        "rename_connection",
			Description: "Rename a saved connection; destination must not exist. Changes configuration only, not existing log filenames or session connection references.",
			Command:     []string{"dbbie", "connect", "--rename", "$old", "$new", "--json"},
			Inputs:      map[string]string{"old": "existing saved name", "new": "unused saved name"},
			Safety:      "atomic private configuration update; retains credentials privately",
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
			Description: "Run read-only SQL with timeout and bounded rows capped at 1000. --why records intent, --title labels the run, --no-log (or DBBIE_NO_LOG=1) disables logging. Returns a per-connection log id; failures are logged too. Actor is DBBIE_ACTOR, else agent with --why or non-terminal stdout, else human. --session NAME (default DBBIE_SESSION) appends successes and failures to a single-connection trail, creating it on first use. Sessions require logging and a saved connection; ad-hoc --save NAME saves credentials privately first.",
			Command:     []string{"dbbie", "query", "--conn", "$connection", "--sql", "$sql", "--why", "$why", "--limit", "$limit", "--json"},
			Inputs:      map[string]string{"connection": "saved connection name (or use --driver and --dsn; log name adhoc)", "sql": "read-only SQL", "limit": "preview row limit, max 1000", "why": "--why TEXT: reason for the query", "title": "optional --title TEXT", "timeout": "optional --timeout duration, default 10s", "no-log": "optional --no-log; retains legacy hash id", "session": "optional --session NAME; defaults to DBBIE_SESSION, requires saved --conn and logging", "save": "optional --save NAME with --driver/--dsn, then logs under saved name", "allow-write": "optional --allow-write explicitly disables read-only protection"},
			Safety:      "read-only by default; bounded preview; rejects multi-statement SQL",
		},
		{
			Name:        "create_session",
			Description: "Create a named ordered query trail on one saved connection; error if it exists. Names match ^[a-z0-9][a-z0-9._-]{0,63}$.",
			Command:     []string{"dbbie", "session", "new", "$session", "--conn", "$connection", "--json"},
			Inputs:      map[string]string{"session": "session name", "connection": "saved connection name", "title": "optional --title TEXT"},
			Safety:      "private local store only; no DSNs; executes no SQL",
		},
		{
			Name:        "list_sessions",
			Description: "List session name, title, conn, item count, updated and createdBy, sorted by updated descending.",
			Command:     []string{"dbbie", "session", "ls", "--json"},
			Inputs:      map[string]string{},
			Safety:      "local store only; executes no SQL",
		},
		{
			Name:        "show_session",
			Description: "Read a session's ordered SQL/why/title/logId items and latest stored result summaries (rowCount, error). Never re-runs SQL or includes rows.",
			Command:     []string{"dbbie", "session", "show", "$session", "--json"},
			Inputs:      map[string]string{"session": "session name"},
			Safety:      "local session and query log only",
		},
		{
			Name:        "add_session_item",
			Description: "Pin an existing entry from the session connection's query log, copying sql, why and title. Item numbers are stable and not reused.",
			Command:     []string{"dbbie", "session", "add", "$session", "$id", "--json"},
			Inputs:      map[string]string{"session": "session name", "id": "q_ log id on the same connection", "title": "optional --title TEXT override"},
			Safety:      "local store only; never executes SQL",
		},
		{
			Name:        "remove_session",
			Description: "Delete a session, or remove just one item with --item N without renumbering the others. Query logs are retained.",
			Command:     []string{"dbbie", "session", "rm", "$session", "--json"},
			Inputs:      map[string]string{"session": "session name", "item": "optional --item positive stable number"},
			Safety:      "local session only; no database changes",
		},
		{
			Name:        "run_session",
			Description: "Re-run ordered items read-only; log every success, failure and rejection with the session name and update each logId. Continues after item failures and exits nonzero if any failed. JSON is one object with items (n, logId, result, error) and a top-level error on failure. DBBIE_NO_LOG does not disable session re-run logging. A human with the session open in the TUI sees each item's tab refresh in place; use this (or --only N) to update what they are looking at.",
			Command:     []string{"dbbie", "session", "run", "$session", "--json"},
			Inputs:      map[string]string{"session": "session name", "only": "optional --only positive stable item number", "limit": "optional --limit N, default 100, max 1000", "timeout": "optional --timeout duration, default 10s per item"},
			Safety:      "always read-only, timeout-bound and bounded; no --allow-write",
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
			Inputs:      map[string]string{"connection": "saved connection name (or use --driver and --dsn)", "save": "optional --save NAME with --driver/--dsn; saves privately then opens under saved name", "theme": "midnight|light|contrast", "sql": "optional --sql initial read-only query", "limit": "optional --limit preview size, max 1000"},
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
