# dbbie

`dbbie` is a Go terminal database cockpit for Postgres and MySQL. It is designed for humans and AI agents: the same core can browse catalog metadata, run bounded read-only queries, open result handles, and render a terminal UI.

## Current Commands

```sh
dbbie --help
dbbie connect prod postgres "$DATABASE_URL"
dbbie connect --rename prod staging
dbbie connect --rm staging
dbbie connections --json
dbbie tables --driver postgres --dsn "$DATABASE_URL" --json
dbbie tables --conn prod --match 'public.user*' --json
dbbie describe --driver mysql --dsn "$MYSQL_DSN" --table users --json
dbbie query --driver postgres --dsn "$DATABASE_URL" --save local --sql "select 1" --json
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

## Named query sessions

A session is an ordered query trail on **one saved connection**. Agents can leave
SQL, intent, labels, and stored result handles for a human to inspect later:

```sh
dbbie session new refund-bug --conn local --title "Why refunds are stuck"
export DBBIE_SESSION=refund-bug
dbbie query --conn local --why "check database context" --title "context" --sql "select current_database()"
dbbie query --conn local --why "count recent refunds" --title "recent refunds" --sql "select count(*) from refunds"
# Rejected SQL is retained too; this command exits nonzero and performs no write.
dbbie query --conn local --why "verify the read-only guard" --sql "delete from refunds"
dbbie session ls
dbbie session show refund-bug
# Explicitly re-executes SQL, always read-only; rejected items still exit nonzero.
dbbie session run refund-bug --limit 100 --timeout 10s
dbbie session show refund-bug
unset DBBIE_SESSION
```

`query --session NAME` overrides `DBBIE_SESSION` (use `--session=` to opt out for
one query). The session is auto-created on first use, with the saved `--conn`.
A connection mismatch is `invalid` and executes no SQL. Sessions require query
logging: combining a session with `--no-log` or `DBBIE_NO_LOG=1` is `invalid`.
Ad-hoc credentials need `--save NAME` before they can be used with a session.

Session names match `^[a-z0-9][a-z0-9._-]{0,63}$`: lowercase, at most 64 characters,
starting with a letter or digit. Commands default to JSON off-terminal and accept
`--json=false` for text:

- `session new NAME --conn X [--title T]` creates an empty trail; an existing name is an error.
- `session ls` lists name/title/conn/item count/updated/createdBy, newest updated first.
- `session show NAME` reads SQL and intent plus each item's latest `rowCount` and
  `error`, **without re-running SQL or printing rows**. Use `show LOG_ID --conn X`
  to page the stored row preview.
- `session add NAME LOG_ID [--title T]` pins an existing log entry from that
  session's connection, copying SQL, why, and title (optionally overridden).
- `session rm NAME --item N` removes one item; `session rm NAME` deletes the session.
  Item numbers are stable and never reused. Query logs are retained.
- `session run NAME [--only N] [--limit N] [--timeout DURATION]` executes items
  in order through the same read-only safety path as `query`, logs every run with
  the session name, and updates `logId`, including failures and rejections. It
  continues after a failed item and exits nonzero if any fail. JSON is one object
  containing `items` (`n`, `logId`, `result`, `error`), plus the usual top-level
  `error` on failure. It always logs, regardless of `DBBIE_NO_LOG`; there is no
  write override. `--only` selects a stable item number, not its current position.

Sessions live at `<stateDir>/sessions/NAME.json`, using the same state-directory
resolution as query logs. Files are `0600`, directories `0700`. Atomic replacement
under a stable per-session lock protects concurrent CLI processes. The extra
`nextN` field preserves the item-number high-water mark after deletion. Sessions
contain no DSNs; SQL and intent can still contain sensitive business information.

## Saved-connection management

```sh
dbbie connect local postgres "$DATABASE_URL"
dbbie connections                         # names and drivers only
dbbie connect --rename local scratch
dbbie connect --rm scratch
dbbie query --driver postgres --dsn "$DATABASE_URL" --save local --sql "select 1"
dbbie open --driver postgres --dsn "$DATABASE_URL" --save local
```

`connect` and ad-hoc `--save` upsert a name. `--save` requires `--driver`/`--dsn`,
not `--conn`; subsequent query logs and TUI opens use the saved name. Rename fails
if the destination exists; removing or renaming an unknown name is `invalid`.
Rename/remove change **only configuration**: existing query logs and session
connection references are not rewritten or deleted. Keep a session's saved name
available if you want to re-run it.

Credentials are stored only in `config.json` (`0600`, parent directory `0700`),
by default `~/.config/dbbie/config.json`; `DBBIE_CONFIG` overrides that path.
Configuration updates are atomic and locked across read-modify-write. CLI output,
query logs, session files, and the tools manifest never include credentials.

## Safety Defaults

- `query` is read-only by default.
- result previews are bounded with `--limit` and capped at 1000 rows.
- query execution is timeout-bound.
- JSON output is stable enough for agents to parse.
