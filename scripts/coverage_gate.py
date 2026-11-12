#!/usr/bin/env python3
"""覆盖率门禁：整体 ≥85%，且核心业务包每个 ≥85%。

用法：python scripts/coverage_gate.py cover.out
用于 CI（.github/workflows/ci.yml）与本地 make coverage。
"""
import sys
from collections import defaultdict

TOTAL_GATE = 85.0
CORE_PKGS = {
    "/internal/api",
    "/internal/cache",
    "/internal/model",
    "/internal/service",
    "/internal/store",
}
CORE_GATE = 85.0


def load(path):
    total = defaultdict(int)
    cov = defaultdict(int)
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("mode:"):
                continue
            parts = line.split(" ")
            if len(parts) != 3:
                continue
            loc, stmts, hit = parts[0], int(parts[1]), int(parts[2])
            if loc.startswith("small-commerce-order/"):
                rel = loc[len("small-commerce-order"):]  # /internal/api/server.go
            else:
                rel = "/main.go"
            pkg = rel.rsplit("/", 1)[0] or "/main"
            total[pkg] += stmts
            if hit > 0:
                cov[pkg] += stmts
    return total, cov


def main():
    if len(sys.argv) != 2:
        print("用法：coverage_gate.py <coverprofile>")
        sys.exit(2)
    total, cov = load(sys.argv[1])

    grand_s = grand_c = 0
    rows = []
    for pkg in sorted(total):
        s, c = total[pkg], cov[pkg]
        grand_s += s
        grand_c += c
        pct = 100.0 * c / s if s else 0.0
        gate = CORE_GATE if pkg in CORE_PKGS else None
        ok = gate is None or pct >= gate
        rows.append((pkg, s, c, pct, gate, ok))
        print(f"{pkg:30s} stmts={s:5d} cov={c:5d} {pct:6.2f}%" + (f"  (gate {gate}%) {'PASS' if ok else 'FAIL'}" if gate else ""))

    total_pct = 100.0 * grand_c / grand_s if grand_s else 0.0
    print(f"{'TOTAL':30s} stmts={grand_s:5d} cov={grand_c:5d} {total_pct:6.2f}%  (gate {TOTAL_GATE}%) {'PASS' if total_pct >= TOTAL_GATE else 'FAIL'}")

    failed = [r for r in rows if not r[5]]
    if total_pct < TOTAL_GATE or failed:
        for r in failed:
            print(f"门禁未达标：{r[0]} {r[3]:.2f}% < {r[4]}%", file=sys.stderr)
        sys.exit(1)
    print("覆盖率门禁通过")


if __name__ == "__main__":
    main()
