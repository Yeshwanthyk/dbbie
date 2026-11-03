# Redesign contracts

These are the shared interfaces between the redesign slices. Change them only through the coordinator.
The visual and interaction target is the mockup at `docs/redesign/mockup.html`. Open it in a browser
and drive it with vim keys.

## Slices and ownership

| Slice | Owns (only these paths) | Depends on |
|---|---|---|
| A · Agent CLI + query log | `internal/qlog/**` (new), `internal/cli/**`, `internal/agent/**`, `README.md` | contracts |
| B · Catalog row counts | `internal/catalog/**` | contracts |
| C1 · TUI shell | `internal/ui/**`, `internal/theme/**` | contracts |
| C2 · Results grid | `internal/ui/**` | C1, B |
| D · Live follow | `internal/ui/**`, `internal/cli/open_stub.go`, `internal/cli/cli.go` (only the `open` flags) | A, C2 |

Guardrails from `LOG.md` still hold. `tview` owns the live TUI. Queries are read-only by default and bounded
(at most 1000 rows). DSNs never appear in output, logs, or JSON.

## 1. catalog.Table.EstimatedRows (already added)

```go
type Table struct {
    Schema, Name, Type string
    EstimatedRows int64 `json:"estimatedRows"` // planner estimate; -1 = unknown
}
```

B fills it in. Postgres: `pg_class.reltuples`; when that is `-1` (never analyzed), fall back to
`pg_stat_user_tables.n_live_tup`; when neither exists, `-1`. MySQL: `information_schema.tables.table_rows`;
NULL becomes `-1`. Views are `-1`. C1 renders `>=0` with a compact count (`2.2k`) and `0` as `empty`
(dimmed). It renders `-1` as nothing.

## 2. Query log (package `internal/qlog`, owned by A)

- **Location:** `$DBBIE_STATE_DIR`, else `$XDG_STATE_HOME/dbbie`, else `~/.local/state/dbbie`. There is one
  append-only JSON Lines file per connection, `<conn>.jsonl`. Ad-hoc `--driver/--dsn` runs use the name
  `adhoc`. The directory is mode `0700` and files are `0600`.
- **One JSON object per line:**

```json
{"v":1,"id":"q_0042","ts":"2026-11-03T11:20:00.123Z","conn":"local","driver":"postgres",
 "source":"cli","actor":"agent","title":"orphan businesses","why":"find businesses with no balance account",
 "sql":"select ...","columns":[{"name":"id","databaseType":"UUID"}],"rows":[["..."]],
 "rowCount":2,"truncated":false,"elapsedMs":14,"error":null}
```

  - `id` is `q_` plus a zero-padded sequence, unique per connection file. Allocation must be safe with
    concurrent writers: hold an exclusive `flock` on the file while you read the last id and append.
  - `source` is `cli` or `tui`. `actor` is `$DBBIE_ACTOR` when set, else `agent` when `--why` is given or
    stdout is not a terminal, else `human`.
  - `rows` is the preview the run returned. It is bounded by the run's limit, never more than 1000.
  - `error` is `null`, or `{"code":"rejected|timeout|db|invalid","message":"..."}`. Rejected (read-only
    violation) runs are logged too, with no rows.
  - `--no-log` or `DBBIE_NO_LOG=1` skips writing.
- **Go API.** These are the exact names other slices consume:

```go
package qlog
type Entry struct { /* fields above; JSON tags as shown */ }
type Log struct { /* opaque */ }
func Open(conn string) (*Log, error)                     // resolves the dir, creates it if needed
func (l *Log) Append(e Entry) (Entry, error)             // assigns ID and TS; returns the stored entry
func (l *Log) List(limit int) ([]Entry, error)           // newest last; limit<=0 means all
func (l *Log) Get(id string) (Entry, error)              // ErrNotFound when missing
func (l *Log) Follow(ctx context.Context, fromOffset int64, fn func(Entry)) error
                                                        // tails new lines until ctx is done; polling every 250ms is fine
func (l *Log) Size() (int64, error)                      // current byte offset, a starting point for Follow
var ErrNotFound error
```

## 3. Agent CLI (A)

- `dbbie query ... [--why TEXT] [--title TEXT] [--no-log]` logs every run. JSON output gains `"id"`
  set to the log id (falling back to the old hash id when logging is off).
- `dbbie log (--conn NAME) [--limit N] [--json]` lists recent entries without rows (id, ts, actor, title,
  why, sql, rowCount, error).
- `dbbie show <id> (--conn NAME) [--offset N] [--limit N] [--json]` returns a stored entry with a row
  page. It does not re-run the query.
- `dbbie tables ... [--match GLOB]` filters on `schema.name` or `name`. The JSON includes `estimatedRows`.
- When stdout is not a terminal, `--json` is the default; `--json=false` overrides it.
- In JSON mode, errors go to stdout as `{"error":{"code":"...","message":"..."}}` with a non-zero exit code.
- `dbbie tools --json` describes every command above.

## 4. TUI (C1, then C2, then D)

The target is the mockup. Keys:

- **Anywhere:** `Ctrl-h`/`Ctrl-l` (sidebar / results), `gt` `gT` `1`-`9` (tabs), `t` (table finder), `i`
  (edit SQL), `:` (`:q` closes the tab, `:qa` quits, raw SQL), `?` (help), `q` (quit only in normal mode
  with one tab, else close the tab), `Ctrl-c` (cancel a running query; quit when idle).
- **Sidebar:** `j` `k` `gg` `G`, `/` (fuzzy filter on names), `Enter` (open in the current tab; agent tabs
  are never replaced, so it opens a new tab), `o` (new tab), `K` (describe columns).
- **Results:** `h` `j` `k` `l`, `w` `b` `0` `$`, `gg` `G` `Ctrl-d` `Ctrl-u`, `Enter` (record view), `/` `n`
  `N` (row filter), `yy` `Y` (copy the cell / the row as JSON, via OSC52), `s` `S` (sort), `f` (re-query
  with `where col = value`), `]a` `[a` (agent tabs).

There is one tab type, defined by C1 in `internal/ui/tabs.go`. It holds a title, the SQL, an optional
`*qlog.Entry` origin (D sets it for agent tabs), the last result or error, the cursor position, the row
filter text, and a running-query cancel func. Queries run off the UI goroutine through
`app.QueueUpdateDraw`. Tests use a synchronous runner hook.

## 5. Sessions and saved connections (F1 store + CLI, then F2 TUI)

A **session** is a named, ordered list of queries on one saved connection, which an agent or a human
can come back to, read, and re-run. Sessions span one connection only; each item records it anyway.

### Store (package `internal/session`, owned by F1)

- File per session: `<stateDir>/sessions/<name>.json`, where stateDir is the same as qlog's
  (`$DBBIE_STATE_DIR`, else `$XDG_STATE_HOME/dbbie`, else `~/.local/state/dbbie`). Directory 0700, files 0600.
  Writes are atomic (temp file + rename) under a per-session flock.
- Name: `^[a-z0-9][a-z0-9._-]{0,63}$`. Anything else is `invalid`.
- JSON, never containing a DSN:

```json
{"v":1,"name":"refund-bug","title":"Why refunds stuck","conn":"local","driver":"postgres",
 "createdBy":"agent","created":"…","updated":"…",
 "items":[{"n":1,"title":"pending by age","why":"…","sql":"select …","logId":"q_0042",
           "added":"…","addedBy":"agent"}]}
```

- `n` is 1-based and stable (removing an item doesn't renumber the rest).
- `logId` points at the latest qlog entry for that item (the stored result). Re-running updates it.
- API: `Open(name) (*Session, error)`, `Create(name, title, conn, driver, actor) (*Session, error)`
  (`ErrExists`), `List() ([]Summary, error)` (sorted by updated desc), `Add(name, Item) (Item, error)`,
  `Remove(name, n) error`, `SetLogID(name, n, logID) error`, `Delete(name) error`, `ErrNotFound`.
- qlog `Entry` gains `"session": "<name>"` (omitempty; v stays 1; old lines still parse).

### CLI (F1)

- `dbbie session new <name> --conn X [--title T]`
- `dbbie session ls [--json]` → name, title, conn, items, updated, createdBy.
- `dbbie session show <name> [--json]` → session plus each item's stored result summary (rowCount, error).
- `dbbie session add <name> <logId> [--title T]` pins an existing log entry (copies its sql/why/title).
- `dbbie session rm <name> [--item N]`
- `dbbie session run <name> [--only N] [--limit] [--timeout]` re-runs items read-only, logs each run with
  `session`, updates `logId`, prints per-item results (JSON default when not a TTY).
- `dbbie query … --session <name>` (or env `DBBIE_SESSION`) appends the query as an item after it runs,
  creating the session on first use with that `--conn`. A failed/rejected query is still added (with its
  error) so the trail is complete. The session conn must match `--conn`, else `invalid`.
- Connections: `dbbie connect <name> <driver> <dsn>` (exists); add `dbbie connect --rm <name>`,
  `dbbie connect --rename <old> <new>`; `dbbie open/query --driver D --dsn X --save <name>` saves it.
  `dbbie connections` never prints DSNs. Config file mode 0600, dir 0700.
- `dbbie tools` manifest and README document all of the above for agents.

### TUI (F2)

- `dbbie open --session <name>`: one tab per item in order, rendered from the stored log entry (no re-run),
  tab title = item title or `#n`, why line shows `session <name> · #n · why`.
- `r` re-runs the current tab's SQL in place (logged; if the tab belongs to a session, updates that item).
- `:save <name> [title]` saves open tabs (SQL + latest log ids) as a session; `:add` adds the current tab
  to the active session. `:sessions` opens a fuzzy picker (Enter opens the session's tabs).
- `:conn` opens a fuzzy picker of saved connections; switching reloads the catalog and keeps tabs.
  `dbbie open` with no `--conn` shows the picker first (with one saved connection, it just opens it).
- Agent tabs from `--follow` show their session name when the entry has one.
