# dbbie

`dbbie` is a Go terminal database cockpit for Postgres and MySQL. It is designed for humans and AI agents: the same core can browse catalog metadata, run bounded read-only queries, open result handles, and render a terminal UI.

## Current Commands

```sh
dbbie --help
dbbie connect prod postgres postgres://user:pass@localhost:5432/app
dbbie connections --json
dbbie tables --driver postgres --dsn "$DATABASE_URL" --json
dbbie tables --conn prod --json
dbbie describe --driver mysql --dsn "$MYSQL_DSN" --table users --json
dbbie query --driver postgres --dsn "$DATABASE_URL" --sql "select * from users" --json
dbbie query --conn prod --sql "select * from users" --json
dbbie open --conn prod
dbbie open --conn prod --sql "select * from users"
dbbie tools --json
```

## Safety Defaults

- `query` is read-only by default.
- result previews are bounded with `--limit` and capped at 1000 rows.
- query execution is timeout-bound.
- JSON output is stable enough for agents to parse.
