#!/usr/bin/env python3
"""逐功能点测试闸门（新增规则一的执行器）。

AI 开发完一个功能点后必须运行本脚本；只有 result=PASS 才能进入下一功能点/阶段。
检查：前置功能点 → 模块单测存在性 → 专项测试文件 → gofmt/vet → 该功能点单测
      →（交付点）覆盖率 / HTTP 验收 → 证据落盘。

用法：
    python scripts/feature_gate.py --list
    python scripts/feature_gate.py --feature F3.1 --dry-run
    python scripts/feature_gate.py --feature F3.1
    python scripts/feature_gate.py --stage 3
退出码：0 = PASS（允许推进）；1 = FAIL（禁止推进）；2 = 用法/环境错误。
"""
import argparse
import json
import os
import re
import subprocess
import sys
import urllib.request
from datetime import datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
FEATURES_FILE = ROOT / ".agent" / "workflow" / "features.json"
RECORDS = ROOT / ".agent" / "workflow" / "gate-records"
CHECK_TESTS = ROOT / "scripts" / "check_module_tests.py"
BASE_URL = "http://127.0.0.1:8090"


def load_features():
    if not FEATURES_FILE.is_file():
        print(f"缺少功能点清单：{FEATURES_FILE}", file=sys.stderr)
        sys.exit(2)
    try:
        data = json.loads(FEATURES_FILE.read_text(encoding="utf-8"))
    except json.JSONDecodeError as e:
        print(f"features.json 解析失败：{e}", file=sys.stderr)
        sys.exit(2)
    return data["features"]


def find(features, fid):
    for f in features:
        if f["id"] == fid:
            return f
    return None


def record_path(fid):
    return RECORDS / f"{fid}.json"


def dep_result(fid):
    p = record_path(fid)
    if not p.is_file():
        return "MISSING"
    try:
        return json.loads(p.read_text(encoding="utf-8")).get("result", "UNKNOWN")
    except Exception:
        return "UNKNOWN"


def run_cmd(cmd, name, timeout=900, expect_empty=False):
    cmd = [sys.executable if c == "python" else c for c in cmd]
    try:
        p = subprocess.run(cmd, cwd=str(ROOT), capture_output=True, text=True,
                           timeout=timeout, encoding="utf-8", errors="replace")
    except FileNotFoundError:
        return {"name": name, "cmd": " ".join(cmd), "exit": 127, "ok": False,
                "output_tail": f"命令不存在：{cmd[0]}（请确认 Go / Python 已加入 PATH）"}
    except subprocess.TimeoutExpired:
        return {"name": name, "cmd": " ".join(cmd), "exit": 124, "ok": False,
                "output_tail": f"命令超时（{timeout}s）"}
    out = (p.stdout or "") + (p.stderr or "")
    ok = p.returncode == 0
    if expect_empty and (p.stdout or "").strip():
        ok = False
    return {"name": name, "cmd": " ".join(cmd), "exit": p.returncode, "ok": ok,
            "output_tail": out[-1200:]}


def check_service():
    try:
        with urllib.request.urlopen(BASE_URL + "/api/meta", timeout=3) as r:
            return r.status == 200
    except Exception:
        return False


def plan(f, features, skip_deps=False):
    """返回该功能点将要执行的检查项（命令列表 + 说明）。"""
    items = []
    fid = f["id"]

    for dep in f.get("requires", []):
        items.append({"kind": "dep", "name": f"前置 {dep}",
                      "note": "读 gate-records/%s.json 要求 result=PASS" % dep})

    items.append({"kind": "cmd", "name": "模块单测存在性",
                  "cmd": [sys.executable, str(CHECK_TESTS), "--json", "--include-frontend"]})

    for tf in f.get("test_files", []):
        p = ROOT / tf
        items.append({"kind": "file", "name": f"专项测试文件 {tf}", "path": str(p),
                      "ok": p.is_file() and p.stat().st_size > 0})

    modules = f.get("modules", [])
    if f.get("type") == "unit":
        target = modules if modules else ["."]
        items.append({"kind": "cmd", "name": "gofmt -l",
                      "cmd": ["gofmt", "-l"] + target, "expect_empty": True})
        items.append({"kind": "cmd", "name": "go vet", "cmd": ["go", "vet", "./..."]})
        if modules and f.get("run"):
            items.append({"kind": "cmd", "name": "功能点单元测试（-race, -count=1, -v）",
                          "cmd": ["go", "test", "-v", "-race", "-count=1",
                                  "-run", f["run"]] + modules})
        elif modules:
            items.append({"kind": "cmd", "name": "模块单元测试（-race, -count=1, -v）",
                          "cmd": ["go", "test", "-v", "-race", "-count=1"] + modules})

    for c in f.get("commands", []):
        items.append({"kind": "cmd", "name": c.get("name", "命令"),
                      "cmd": c["cmd"], "expect_empty": c.get("expect_empty", False)})

    if f.get("needs_service"):
        items.append({"kind": "service", "name": "验收服务可达性", "note": BASE_URL + "/api/meta"})
    return items


def execute(f, features, dry_run=False):
    fid = f["id"]
    items = plan(f, features)
    checks = []
    tests_hit = 0
    ok = True

    print(f"\n=== 功能点闸门 {fid} · {f['title']} ===")
    print(f"规则锚点：{f.get('rule_ref', '—')} | 前置：{', '.join(f.get('requires', [])) or '无'}")
    print(f"模块：{', '.join(f.get('modules', [])) or '—'}")
    for a in f.get("assertions", []):
        print(f"  断言：{a}")

    env_required = f.get("env_required")
    if env_required and not os.environ.get(env_required):
        print(f"SKIP：未设置环境变量 {env_required}，本点为可选集成点，不计为通过。")
        return {"feature": fid, "title": f["title"], "result": "SKIP",
                "at": datetime.now().astimezone().isoformat(timespec="seconds"),
                "checks": [], "tests_hit": 0, "reason": f"missing env {env_required}"}

    if dry_run:
        print("DRY-RUN（不执行、不写证据、不算通过）：")
        for it in items:
            if it["kind"] == "cmd":
                print("  $ " + " ".join(it["cmd"]))
            elif it["kind"] == "dep":
                print("  [前置] " + it["name"] + " → " + it["note"])
            elif it["kind"] == "file":
                print(f"  [文件] {it['name']} → {'存在' if it['ok'] else '缺失'}")
            else:
                print("  [服务] " + it["note"])
        return None

    for it in items:
        if it["kind"] == "dep":
            dep = it["name"].replace("前置 ", "")
            r = dep_result(dep)
            good = r == "PASS"
            checks.append({"name": it["name"], "exit": 0 if good else 1,
                           "ok": good, "output_tail": f"gate-records/{dep}.json → {r}"})
        elif it["kind"] == "file":
            good = bool(it["ok"])
            checks.append({"name": it["name"], "exit": 0 if good else 1,
                           "ok": good, "output_tail": "存在" if good else "文件缺失或为空"})
        elif it["kind"] == "service":
            good = check_service()
            checks.append({"name": it["name"], "exit": 0 if good else 1, "ok": good,
                           "output_tail": "服务可达" if good else
                           f"服务不可达：请先启动 `go run .`，再重跑本闸门（{BASE_URL}）"})
        else:
            r = run_cmd(it["cmd"], it["name"], expect_empty=it.get("expect_empty", False))
            checks.append(r)
            if "go test" in " ".join(it["cmd"][:2]) and "-v" in it["cmd"]:
                tests_hit += len(re.findall(r"^=== RUN\s+", r.get("output_tail", ""), re.M))

        c = checks[-1]
        print(f"  [{'PASS' if c['ok'] else 'FAIL'}] {c['name']}")
        if not c["ok"]:
            ok = False
            print("        " + (c.get("output_tail") or "").strip().splitlines()[-1][:160]
                  if (c.get("output_tail") or "").strip() else "")
            break

    if ok and f.get("type") == "unit" and f.get("modules") and f.get("run"):
        if tests_hit == 0:
            ok = False
            checks.append({"name": "测试命中数 > 0", "exit": 1, "ok": False,
                           "output_tail": "go test 未命中任何测试（0 tests ran），视为 FAIL"})
            print("  [FAIL] 测试命中数 > 0：0 tests ran")

    result = "PASS" if ok else "FAIL"
    rec = {"feature": fid, "title": f["title"], "stage": f.get("stage"),
           "result": result, "at": datetime.now().astimezone().isoformat(timespec="seconds"),
           "requires": f.get("requires", []), "checks": checks, "tests_hit": tests_hit}
    RECORDS.mkdir(parents=True, exist_ok=True)
    record_path(fid).write_text(json.dumps(rec, ensure_ascii=False, indent=2), encoding="utf-8")

    print(f"\n闸门结果：{result} | 证据：.agent/workflow/gate-records/{fid}.json")
    if result == "FAIL":
        print("禁止进入下一功能点。下一步：先写 .agent/general/issue-log.md → 只修本功能点 → 原命令重跑。")
    else:
        print("允许：更新 .agent/workflow/progress.md → 按 general/git-convention.md 提交 → 取下一个功能点。")
    return rec


def list_features(features):
    print(f"{'ID':7s} {'阶段':4s} {'类型':7s} {'前置':22s} 功能点")
    print("-" * 96)
    for f in features:
        tag = "可选" if f.get("optional") else f.get("type", "unit")
        print(f"{f['id']:7s} {f.get('stage', 0):<4d} {tag:7s} "
              f"{','.join(f.get('requires', []))[:22]:22s} {f['title']}")
    print("-" * 96)
    print("单点闸门：python scripts/feature_gate.py --feature <ID>")
    print("阶段闸门：python scripts/feature_gate.py --stage <阶段号>")


def main():
    ap = argparse.ArgumentParser(description="逐功能点测试闸门")
    ap.add_argument("--list", action="store_true", help="列出全部功能点")
    ap.add_argument("--feature", help="跑单个功能点闸门，如 F3.1")
    ap.add_argument("--stage", type=int, help="按顺序跑整个阶段的功能点")
    ap.add_argument("--dry-run", action="store_true", help="只打印将执行的检查，不执行也不写证据")
    args = ap.parse_args()

    features = load_features()
    if args.list:
        list_features(features)
        return 0
    if not args.feature and args.stage is None:
        ap.print_help()
        return 2

    if args.feature:
        f = find(features, args.feature)
        if not f:
            print(f"未知功能点：{args.feature}", file=sys.stderr)
            return 2
        rec = execute(f, features, args.dry_run)
        return 0 if (args.dry_run or rec["result"] in ("PASS", "SKIP")) else 1

    targets = [f for f in features if f.get("stage") == args.stage and not f.get("optional")]
    if not targets:
        print(f"阶段 {args.stage} 没有功能点", file=sys.stderr)
        return 2
    print(f"阶段 {args.stage} 共 {len(targets)} 个功能点，按顺序逐个闸门；任一 FAIL 立即停止。")
    for f in targets:
        rec = execute(f, features, args.dry_run)
        if args.dry_run:
            continue
        if rec["result"] == "FAIL":
            print(f"\n阶段 {args.stage} 在 {f['id']} 处中断。", file=sys.stderr)
            return 1
    if not args.dry_run:
        print(f"\n阶段 {args.stage} 全部功能点闸门通过。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
