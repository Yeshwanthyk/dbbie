# dbbie Implementation Log

## Mission

Build `dbbie`, a Go-based, DB-agnostic TUI/CLI for Postgres and MySQL that is AI-first, fast on large result sets, themeable, and verifiable through tests and review gates.

## Done Criteria

- [x] CLI exists with human-friendly and JSON/agent-friendly commands.
- [x] Postgres and MySQL are both first-class through a shared driver/catalog/query abstraction.
- [x] Query execution is cancellable, bounded, and returns structured result handles/previews.
- [x] TUI uses tview for dense widgets and Charm libraries for model/theme surfaces.
- [x] UI supports clean themes and Vim-first movement.
- [x] Agent/MCP-style control surface is represented in CLI/tool contracts.
- [x] Focused test harness covers core contracts, query safety, catalog behavior, themes, and UI key model.
- [x] Each implementation chunk gets a review subagent pass and fixes are tracked.
- [x] Performance-sensitive paths avoid unbounded row loading.

## Guardrails

- Do not make the core Postgres-specific.
- Read-only query path must be safe by default.
- Do not dump unbounded row data into terminal output or agent JSON.
- Keep terminal runtime composition honest: tview owns the live TUI unless a Bubble Tea spike proves otherwise.

## Review Gates

| Chunk | Status | Reviewer | Notes |
|---|---:|---|---|
| Scaffold + core CLI | Done | Hooke, Curie | Fixed compile issues, read-only transaction, server-side preview cap, MySQL schema default, DSN redaction, MCP stub failure. |
| Themeable TUI shell | Done | Mill, Mendel | tview runtime, Vim key capture, Bubble Tea key model, Lip Gloss theme tokens, global tview theme backgrounds. |
| AI CLI contract | Done | Mill | Machine-readable tool manifest and saved `--conn` flow for agent discovery. |

## Critical Learnings

- Decision: `tview` will own the live terminal UI initially; Charm Bubble Tea is used for deterministic key/command model tests and Lip Gloss for theme tokens.
- Constraint: JSON output must redact stored DSNs; agents should receive handles/previews, not credentials or unbounded row data.
- Validation: `go test -count=1 ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./cmd/dbbie` pass after final review fixes.
