#!/usr/bin/env python3
"""Drive the dbbie TUI in a headless tmux pane and save each screen as HTML.

Usage:
  scripts/tui-snap.py --cmd "dbbie open --conn local" --out /tmp/snaps \
      --step "start" --step "t|balacct|Enter|Enter=open table" ...

Each --step is "keys=label". Keys are separated by "|". A key is sent with
tmux send-keys, so names like Enter, Escape, C-h and Tab work. Use "lit:TEXT"
to type TEXT literally, and "sleep:0.5" to wait. The first step may be a bare
label (no keys), which captures the startup screen.

--driver rex (the default when `rex` is on PATH) runs the program in a hidden
Rex session sized with the block's `size` option, sends keys with
`rex send-key` (Rex key syntax: enter, escape, ctrl+h, j) and text with
`rex send`, and captures with `rex capture --format=html`. --driver tmux keeps
the tmux path (tmux key names: Enter, Escape, C-h). --png also writes a PNG
of index.html through headless Chrome.

Each step writes <out>/<NN>-<label>.html (a <pre> with the pane's real colors)
and <out>/index.html, which stacks every capture on one page.
"""
import argparse
import html
import os
import re
import subprocess
import time

BASIC = ["#000000", "#cd3131", "#0dbc79", "#e5e510", "#2472c8", "#bc3fbc", "#11a8cd", "#e5e5e5",
         "#666666", "#f14c4c", "#23d18b", "#f5f543", "#3b8eea", "#d670d6", "#29b8db", "#ffffff"]


def xterm256(n):
    if n < 16:
        return BASIC[n]
    if n < 232:
        n -= 16
        steps = [0, 95, 135, 175, 215, 255]
        return "#%02x%02x%02x" % (steps[n // 36], steps[(n // 6) % 6], steps[n % 6])
    v = 8 + (n - 232) * 10
    return "#%02x%02x%02x" % (v, v, v)


def parse_color(params, i):
    """Return (color, next_index) for a 38/48 sequence starting at params[i]."""
    mode = params[i + 1] if i + 1 < len(params) else None
    if mode == 2 and i + 4 < len(params):
        return "#%02x%02x%02x" % tuple(params[i + 2:i + 5]), i + 5
    if mode == 5 and i + 2 < len(params):
        return xterm256(params[i + 2]), i + 3
    return None, i + 1


SGR = re.compile(r"\x1b\[([0-9;:]*)m")


def ansi_to_html(text, fg0="#F0F6FC", bg0="#0D1117"):
    state = {"fg": None, "bg": None, "bold": False, "ul": False, "rev": False, "dim": False, "it": False}
    out = []
    pos = 0

    def emit(chunk):
        if not chunk:
            return
        fg, bg = state["fg"] or fg0, state["bg"]
        if state["rev"]:
            fg, bg = (state["bg"] or bg0), (state["fg"] or fg0)
        style = ["color:" + fg]
        if bg:
            style.append("background:" + bg)
        if state["bold"]:
            style.append("font-weight:700")
        if state["ul"]:
            style.append("text-decoration:underline")
        if state["dim"]:
            style.append("opacity:.6")
        if state["it"]:
            style.append("font-style:italic")
        out.append('<span style="%s">%s</span>' % (";".join(style), html.escape(chunk)))

    for m in SGR.finditer(text):
        emit(text[pos:m.start()])
        pos = m.end()
        raw = m.group(1).replace(":", ";")
        params = [int(p) if p else 0 for p in raw.split(";")] if raw else [0]
        i = 0
        while i < len(params):
            p = params[i]
            if p == 0:
                state.update(fg=None, bg=None, bold=False, ul=False, rev=False, dim=False, it=False)
            elif p == 1:
                state["bold"] = True
            elif p == 2:
                state["dim"] = True
            elif p == 3:
                state["it"] = True
            elif p == 4:
                state["ul"] = True
            elif p == 7:
                state["rev"] = True
            elif p == 22:
                state["bold"] = state["dim"] = False
            elif p == 23:
                state["it"] = False
            elif p == 24:
                state["ul"] = False
            elif p == 27:
                state["rev"] = False
            elif 30 <= p <= 37:
                state["fg"] = BASIC[p - 30]
            elif 90 <= p <= 97:
                state["fg"] = BASIC[p - 90 + 8]
            elif 40 <= p <= 47:
                state["bg"] = BASIC[p - 40]
            elif 100 <= p <= 107:
                state["bg"] = BASIC[p - 100 + 8]
            elif p == 39:
                state["fg"] = None
            elif p == 49:
                state["bg"] = None
            elif p in (38, 48):
                color, i = parse_color(params, i)
                state["fg" if p == 38 else "bg"] = color
                continue
            i += 1
    emit(text[pos:])
    return "".join(out)


def page(title, body, bg="#0D1117"):
    return ("<!doctype html><meta charset=utf-8><title>%s</title>"
            "<style>body{margin:0;padding:12px;background:%s}pre{margin:0;padding:8px;background:%s;"
            "font:12px/1.25 'JetBrains Mono','SF Mono',Menlo,monospace;color:#F0F6FC;white-space:pre}"
            "h3{font:600 12px system-ui;color:#8B949E;margin:14px 8px 4px}</style>%s"
            % (html.escape(title), bg, bg, body))


def rex(*args, inp=None):
    return subprocess.run(["rex", *args], check=True, capture_output=True, text=True, input=inp).stdout


REX_START = """
local a = rex.args
local s = rex.call("session.create", { label = a.label, initial_windows = { { window_label = a.label,
  layout = { block = { flavor = "com.superlogical.terminal.shell",
    options = { command = { "sh", "-c", a.cmd }, shell = "none", size = { columns = a.cols, rows = a.rows } } } } } } })
return { session_id = s.session_id, block_id = s.initial_windows[1].block_ids[1] }
"""


class RexPane:
    def __init__(self, label, cmd, cols, rows):
        import json
        args = json.dumps({"label": label, "cmd": cmd, "cols": cols, "rows": rows})
        out = json.loads(rex("do", "-", "--json", "--args", args, inp=REX_START))
        self.sid, self.bid = out["session_id"], out["block_id"]

    def key(self, k):
        rex("send-key", "-s", self.sid, "-b", self.bid, k)

    def text(self, t):
        rex("send", "-s", self.sid, "-b", self.bid, t)

    def capture(self):
        raw = rex("capture", "-s", self.sid, "-b", self.bid, "--format=html")
        # Rex emits <div style="display: inline;..."> runs; keep its colours.
        return raw.replace('font-family: monospace; white-space: pre;', '')

    def close(self):
        subprocess.run(["rex", "kill", self.sid], capture_output=True)


class TmuxPane:
    def __init__(self, label, cmd, cols, rows):
        self.s = label
        subprocess.run(["tmux", "kill-session", "-t", label], capture_output=True)
        tmux("new-session", "-d", "-s", label, "-x", str(cols), "-y", str(rows),
             "TERM=xterm-256color COLORTERM=truecolor " + cmd)

    def key(self, k):
        tmux("send-keys", "-t", self.s, k)

    def text(self, t):
        tmux("send-keys", "-t", self.s, "-l", t)

    def capture(self):
        return ansi_to_html(tmux("capture-pane", "-p", "-e", "-t", self.s).rstrip("\n"))

    def close(self):
        subprocess.run(["tmux", "kill-session", "-t", self.s], capture_output=True)


def chrome_png(html_path, png_path, width, height):
    chrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
    if not os.path.exists(chrome):
        print("no Chrome; skipped PNG")
        return
    subprocess.run([chrome, "--headless=new", "--disable-gpu", "--hide-scrollbars",
                    "--force-device-scale-factor=2", "--window-size=%d,%d" % (width, height),
                    "--screenshot=" + png_path, "file://" + html_path], capture_output=True)
    print(png_path)


def tmux(*args):
    return subprocess.run(["tmux", *args], check=True, capture_output=True, text=True).stdout


def main():
    import shutil
    ap = argparse.ArgumentParser()
    ap.add_argument("--cmd", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--session", default="dbbie-snap")
    ap.add_argument("--driver", choices=["rex", "tmux"], default="rex" if shutil.which("rex") else "tmux")
    ap.add_argument("--cols", type=int, default=160)
    ap.add_argument("--rows", type=int, default=42)
    ap.add_argument("--settle", type=float, default=0.8)
    ap.add_argument("--png", action="store_true")
    ap.add_argument("--step", action="append", default=[])
    a = ap.parse_args()
    os.makedirs(a.out, exist_ok=True)
    pane = (RexPane if a.driver == "rex" else TmuxPane)(a.session, a.cmd, a.cols, a.rows)
    time.sleep(1.5)
    sections = []
    try:
        for n, step in enumerate(a.step or ["start"]):
            keys, _, label = step.rpartition("=") if "=" in step else ("", "", step)
            for key in [k for k in keys.split("|") if k]:
                if key.startswith("lit:"):
                    pane.text(key[4:])
                elif key.startswith("sleep:"):
                    time.sleep(float(key[6:]))
                else:
                    pane.key(key)
                time.sleep(0.12)
            time.sleep(a.settle)
            body = "<pre>%s</pre>" % pane.capture()
            slug = re.sub(r"[^a-z0-9]+", "-", label.lower()).strip("-") or "step"
            with open(os.path.join(a.out, "%02d-%s.html" % (n, slug)), "w") as f:
                f.write(page(label, body))
            sections.append("<h3>%02d · %s</h3>%s" % (n, html.escape(label), body))
            print("captured %02d %s" % (n, label))
    finally:
        pane.close()
    index = os.path.abspath(os.path.join(a.out, "index.html"))
    with open(index, "w") as f:
        f.write(page("dbbie snaps", "".join(sections)))
    print(index)
    if a.png:
        chrome_png(index, index[:-5] + ".png", int(a.cols * 7.3) + 50, len(sections) * (a.rows * 15 + 60) + 30)


if __name__ == "__main__":
    main()
