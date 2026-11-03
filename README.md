# dbbie

`dbbie` is a Go terminal database cockpit for Postgres and MySQL. It is designed for humans and AI agents: the same core can browse catalog metadata, run bounded read-only queries, open result handles, and render a terminal UI.

## Current Commands

```sh
dbbie --help
dbbie connect prod postgres postgres://user:pass@localhost:5432/app
dbbie connections --json
dbbie tables --driver postgres --dsn "$DATABASE_URL" --json
dbbie tables --conn prod --match 'public.user*' --json
dbbie describe --driver mysql --dsn "$MYSQL_DSN" --table users --json
dbbie query --driver postgres --dsn "$DATABASE_URL" --sql "select * from users" --json
dbbie query --conn prod --why "inspect recent users" --title "recent users" --sql "select * from users" --limit 100 --json
dbbie log --conn prod --limit 20 --json
dbbie show q_0001 --conn prod --offset 0 --limit 25 --json
dbbie open --conn prod
dbbie open --conn prod --sql "select * from users"
dbbie tools --json
```

## Query history and agent output

Data commands default to JSON when stdout is piped or redirected. Use
`--json=false` for readable text, or `--json` to request JSON at a terminal.
Errors in JSON mode are written to stdout as
`{"error":{"code":"rejected|timeout|db|invalid","message":"..."}}` and exit non-zero.
`tools --json` describes all commands; `serve-mcp` is reserved but not implemented.

Every `query` run, including failures and rejected writes, stores a bounded preview
in a private per-connection JSON Lines log. `--why` records intent; `--title` labels
the query. The JSON result's `id` is the stored `q_0001`-style id (unique within that
connection). `--no-log` or `DBBIE_NO_LOG=1` skips logging and retains the legacy hash id.
The actor is `DBBIE_ACTOR` when set, otherwise `agent` when `--why` is supplied or
stdout is not a terminal, otherwise `human`.

`log --conn NAME` lists recent summaries **without rows**, oldest first within the
selected recent entries. Its default limit is 20; `--limit 0` lists all entries.
`show ID --conn NAME` pages stored rows only: it **never re-runs SQL**. Offset defaults
to 0 and limit to 100 (capped at 1000); `rowCount` remains the original preview count.
Offsets past the preview return an empty page, and a zero limit returns no rows.
Ad-hoc `--driver`/`--dsn` queries use the log name `adhoc`.

Logs live in `$DBBIE_STATE_DIR`, else `$XDG_STATE_HOME/dbbie`, else
`~/.local/state/dbbie`. The directory is mode `0700` and files are `0600`.
Completed lines survive a torn final append; the next append discards only the
unfinished suffix before allocating the next id. Stored SQL and row previews can
contain sensitive business data: use `--no-log` when they should not persist.
DSNs are never included in output or logs.

`tables --match GLOB` matches either `name` or `schema.name` with Go's `path.Match`
syntax. Table JSON includes `estimatedRows` (a planner estimate; `-1` means unknown).

## Safety Defaults

- `query` is read-only by default.
- result previews are bounded with `--limit` and capped at 1000 rows.
- query execution is timeout-bound.
- JSON output is stable enough for agents to parse.
