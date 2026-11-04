# Performance harness

Measures dbbie with [strobe](https://github.com/Hona/strobe) (a dev dependency: `bun install`, then `bunx strobe`).
Everything runs against a private Postgres and private dbbie state, so your own config is untouched.

## Fixture

```sh
PG=/opt/homebrew/opt/postgresql@17/bin; D=/tmp/dbbie-perf/pg
$PG/initdb -D $D -U postgres --auth=trust
$PG/pg_ctl -D $D -o "-p 55432 -k /tmp/dbbie-perf" -l /tmp/dbbie-perf/pg.log start
$PG/psql -h /tmp/dbbie-perf -p 55432 -U postgres -c 'create database perf'
$PG/psql -h /tmp/dbbie-perf -p 55432 -U postgres -d perf -f perf/seed.sql

export DBBIE_CONFIG=/tmp/dbbie-perf/config/config.json DBBIE_STATE_DIR=/tmp/dbbie-perf/state
DSN="postgres://postgres@localhost:55432/perf?sslmode=disable"
go build -o /tmp/dbbie-perf/dbbie ./cmd/dbbie && B=/tmp/dbbie-perf/dbbie
for c in perf biglog smalllog; do $B connect $c postgres "$DSN"; done
# A 2000-entry query log for biglog (slow: each append rescans the log)
for i in $(seq 1 2000); do $B query --conn biglog --sql "select * from events where id > $((i*300)) order by id" --limit 100 --why seed >/dev/null; done
```

## Grid benchmarks (Go)

```sh
DBBIE_BENCH_DSN="$DSN" go test ./internal/ui -run '^$' -bench Grid -benchmem -count 6 | tee /tmp/grid.txt
benchstat /tmp/grid.txt
```

They skip without `DBBIE_BENCH_DSN`, so the rows always come from the real driver.

## CLI bench (strobe)

```sh
DBBIE_BIN=$B bunx strobe bench perf/cli.bench.ts --head . --check   # each case once
DBBIE_BIN=$B bunx strobe bench perf/cli.bench.ts --base main --head .
```

## TUI, measured from outside

Run dbbie in a 200x50 tmux pane, focus the grid (Tab), send keys with `tmux send-keys`,
and record the dbbie process: `strobe record pid:$(pgrep -x dbbie) --for 8s`.
Check that the cursor moved (`tmux capture-pane`) before trusting the number.
