#!/usr/bin/env python3
"""Evaluate spike measurements and append verdicts to a JSONL results file.

record   --file F --test NAME --kind rtt|loss|range --expect X [--base B] --measured JSON
timeline --file F --test NAME --stream STREAM.jsonl --events EVENTS.txt --base B --added A
"""
import argparse, json, math, statistics, sys


def rtt_ok(measured_median, expect_ms, base_ms):
    target = base_ms + expect_ms
    tol = 3 + 0.05 * expect_ms          # emulated kernel: ±3 ms + 5 %
    return abs(measured_median - target) <= tol, round(target, 2), round(tol, 2)


def loss_ok(sent, lost, p):
    mu = sent * p
    sd = math.sqrt(max(sent * p * (1 - p), 1e-9))
    lo, hi = mu - 2.576 * sd, mu + 2.576 * sd   # 99 % interval
    return lo <= lost <= hi, [round(100 * lo / sent, 2), round(100 * hi / sent, 2)]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("mode"); ap.add_argument("--file", required=True)
    ap.add_argument("--test", required=True); ap.add_argument("--kind", default="rtt")
    ap.add_argument("--expect", type=float, default=0); ap.add_argument("--base", type=float, default=0)
    ap.add_argument("--measured"); ap.add_argument("--stream"); ap.add_argument("--events")
    ap.add_argument("--added", type=float, default=0); ap.add_argument("--note", default="")
    a = ap.parse_args()
    out = {"test": a.test, "note": a.note}

    if a.mode == "record":
        m = json.loads(a.measured)
        out["measured"] = m
        if a.kind == "rtt":
            med = m["rtt_ms"]["median"] if "rtt_ms" in m else m["median"]
            ok, target, tol = rtt_ok(med, a.expect, a.base)
            out.update(expect_median_ms=target, tolerance_ms=tol, median_ms=med, pass_=ok)
        elif a.kind == "loss":
            lost = m["sent"] - m["received"]
            ok, ci = loss_ok(m["sent"], lost, a.expect / 100)
            out.update(expect_loss_pct=a.expect, ci99_pct=ci, loss_pct=m["loss_pct"], pass_=ok)
        elif a.kind == "info":
            out["pass_"] = None
    elif a.mode == "timeline":
        ev = [l.split() for l in open(a.events) if l.strip()]      # "<epoch> <label>"
        rows = [json.loads(l) for l in open(a.stream) if l.strip()]
        rtt = [(r["t"], r["ms"]) for r in rows if r.get("event") == "rtt"]
        other = [r for r in rows if r.get("event") not in ("rtt", "connected", "done")]
        bounds = [float(e[0]) for e in ev]
        phases = []
        edges = [0.0] + bounds + [1e12]
        labels = ["start"] + [e[1] for e in ev]
        for i in range(len(edges) - 1):
            seg = [ms for t, ms in rtt if edges[i] + (0.5 if i else 0) <= t < edges[i + 1]]
            phases.append({"phase": labels[i],
                           "median_ms": round(statistics.median(seg), 2) if seg else None,
                           "n": len(seg)})
        # reaction: is the first message SENT after the change was applied already affected?
        # events file lines: "<applied_epoch> <label> <command_start_epoch>"
        react = []
        sends = [(t - ms / 1000.0, ms) for t, ms in rtt]
        thr = a.base + a.added / 2
        for e in ev:
            ts, label = float(e[0]), e[1]
            cmd_ms = round((ts - float(e[2])) * 1000, 1) if len(e) > 2 else None
            rising = label.startswith("on")
            after = [(s, ms) for s, ms in sends if s >= ts]
            hit = next((s for s, ms in after if (ms > thr) == rising), None)
            unaffected = next((i for i, (s, ms) in enumerate(after) if (ms > thr) == rising), None)
            react.append({"event": label, "command_ms": cmd_ms,
                          "first_affected_send_after_apply_ms": round((hit - ts) * 1000, 1) if hit else None,
                          "unaffected_msgs_after_apply": unaffected})
        out.update(phases=phases, reaction=react, errors=other)
        out["pass_"] = all(r["first_affected_send_after_apply_ms"] is not None and
                           r["first_affected_send_after_apply_ms"] <= 100 for r in react) and not other
    with open(a.file, "a") as f:
        f.write(json.dumps(out) + "\n")
    print(json.dumps(out))


if __name__ == "__main__":
    main()
