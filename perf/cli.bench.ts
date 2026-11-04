import { bench, script } from "@hona/strobe"

// dbbie CLI paths against the perf fixture (perf/README.md). Wall time per call is
// the primary: the dbbie child process does the work, not the Bun host.
const call = (fn: string) => ({
  primary: "phase.call",
  async run({ call, phase }: any) {
    await phase("call", () => call(fn))
  },
})

export default bench({
  targets: { cli: script("cli.ts") },
  cases: {
    "query-nolog": call("queryNoLog"),
    "query-logged-small": call("queryLoggedSmall"),
    "query-logged-2000": call("queryLogged"),
    "log-recent-2000": call("logRecent"),
    "show-page-2000": call("showPage"),
  },
})
