#!/usr/bin/env python3
"""Drive the dbbie TUI in a headless tmux pane and save each screen as HTML.

Usage:
  scripts/tui-snap.py --cmd "dbbie open --conn local" --out /tmp/snaps \
      --step "start" --step "t|balacct|Enter|Enter=open table" ...

Each --step is "keys=label". Keys are separated by "|". A key is sent with
tmux send-keys, so names like Enter, Escape, C-h and Tab work. Use "lit:TEXT"
to type TEXT literally, and "sleep:0.5" to wait. The first step may be a bare
label (no keys), which captures the startup screen.

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
            "<style>body{margin:0;background:%s}pre{margin:0;padding:8px;background:%s;"
            "font:12px/1.25 'JetBrains Mono','SF Mono',Menlo,monospace;color:#F0F6FC;white-space:pre}"
            "h3{font:600 12px system-ui;color:#8B949E;margin:14px 8px 4px}</style>%s"
            % (html.escape(title), bg, bg, body))


def tmux(*args):
    return subprocess.run(["tmux", *args], check=True, capture_output=True, text=True).stdout


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--cmd", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--session", default="dbbie-snap")
    ap.add_argument("--cols", type=int, default=160)
    ap.add_argument("--rows", type=int, default=42)
    ap.add_argument("--settle", type=float, default=0.8)
    ap.add_argument("--step", action="append", default=[])
    a = ap.parse_args()
    os.makedirs(a.out, exist_ok=True)
    subprocess.run(["tmux", "kill-session", "-t", a.session], capture_output=True)
    env_cmd = "TERM=xterm-256color COLORTERM=truecolor " + a.cmd
    tmux("new-session", "-d", "-s", a.session, "-x", str(a.cols), "-y", str(a.rows), env_cmd)
    time.sleep(1.5)
    sections = []
    try:
        for n, step in enumerate(a.step or ["start"]):
            keys, _, label = step.rpartition("=") if "=" in step else ("", "", step)
            for key in [k for k in keys.split("|") if k]:
                if key.startswith("lit:"):
                    tmux("send-keys", "-t", a.session, "-l", key[4:])
                elif key.startswith("sleep:"):
                    time.sleep(float(key[6:]))
                else:
                    tmux("send-keys", "-t", a.session, key)
                time.sleep(0.12)
            time.sleep(a.settle)
            raw = tmux("capture-pane", "-p", "-e", "-t", a.session)
            body = "<pre>%s</pre>" % ansi_to_html(raw.rstrip("\n"))
            slug = re.sub(r"[^a-z0-9]+", "-", label.lower()).strip("-") or "step"
            with open(os.path.join(a.out, "%02d-%s.html" % (n, slug)), "w") as f:
                f.write(page(label, body))
            sections.append("<h3>%02d · %s</h3>%s" % (n, html.escape(label), body))
            print("captured %02d %s" % (n, label))
    finally:
        subprocess.run(["tmux", "kill-session", "-t", a.session], capture_output=True)
    with open(os.path.join(a.out, "index.html"), "w") as f:
        f.write(page("dbbie snaps", "".join(sections)))
    print(os.path.join(a.out, "index.html"))


if __name__ == "__main__":
    main()
