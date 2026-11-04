// CLI workloads for strobe: each export runs one dbbie command and checks it succeeded.
// Env: DBBIE_BIN (the binary), DBBIE_CONFIG and DBBIE_STATE_DIR (the perf fixture, see perf/README.md).
const bin = process.env.DBBIE_BIN ?? "dbbie"

function run(args: string[]): number {
  const p = Bun.spawnSync([bin, ...args, "--json"], { env: process.env, stdout: "pipe", stderr: "pipe" })
  if (p.exitCode !== 0) throw new Error(`dbbie ${args.join(" ")} exited ${p.exitCode}: ${p.stderr.toString()}`)
  const out = JSON.parse(p.stdout.toString())
  if (out.error) throw new Error(JSON.stringify(out.error))
  return p.stdout.length
}

// 1000-row query, no log: driver, read-only guard and JSON output only.
export function queryNoLog() {
  return run(["query", "--conn", "perf", "--sql", "select * from events order by id", "--limit", "1000", "--no-log"])
}

// 100-row query appended to the 2000-entry log: what an agent's query costs with history.
export function queryLogged() {
  return run(["query", "--conn", "biglog", "--sql", "select * from events order by id", "--limit", "100", "--why", "bench"])
}

// Same query appended to a near-empty log.
export function queryLoggedSmall() {
  return run(["query", "--conn", "smalllog", "--sql", "select * from events order by id", "--limit", "100", "--why", "bench"])
}

export function logRecent() {
  return run(["log", "--conn", "biglog", "--limit", "20"])
}

export function showPage() {
  return run(["show", "q_1000", "--conn", "biglog", "--offset", "0", "--limit", "100"])
}
