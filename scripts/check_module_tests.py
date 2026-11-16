#!/usr/bin/env python3
"""模块单元测试存在性门禁（新增规则二的可执行检查）。

规则：internal/ 下每个含业务源码的包都必须有非空 *_test.go；
前端 web/ 采用等价要求（验收脚本覆盖写操作与越权断言）。

用法：
    python scripts/check_module_tests.py
    python scripts/check_module_tests.py --json
    python scripts/check_module_tests.py --include-frontend
退出码：0 = 全部有单测；1 = 存在缺失；2 = 用法错误。
"""
import argparse
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
INTERNAL = ROOT / "internal"
WEB = ROOT / "web"
ACCEPTANCE = ROOT / "scripts" / "acceptance_check.py"


def scan_modules():
    rows = []
    if not INTERNAL.is_dir():
        return rows, ["internal/ 目录不存在"]
    for d in sorted(p for p in INTERNAL.iterdir() if p.is_dir()):
        if d.name in {"testdata", "vendor"}:
            continue
        sources = [f for f in d.glob("*.go") if not f.name.endswith("_test.go")]
        if not sources:
            continue
        tests = sorted(d.glob("*_test.go"))
        ok = any(f.stat().st_size > 0 for f in tests)
        rows.append({
            "module": "internal/" + d.name,
            "sources": len(sources),
            "test_files": [f.name for f in tests],
            "ok": ok,
        })
    missing = [r["module"] for r in rows if not r["ok"]]
    return rows, missing


def scan_frontend():
    """前端等价要求：web/ 有 JS 源码时，验收脚本必须存在且覆盖写操作与越权断言。"""
    js = sorted(WEB.glob("*.js")) if WEB.is_dir() else []
    if not js:
        return {"module": "web/", "ok": True, "note": "无 JS 源码，跳过"}
    if not ACCEPTANCE.is_file():
        return {"module": "web/", "ok": False, "note": "缺少 scripts/acceptance_check.py 等价断言"}
    text = ACCEPTANCE.read_text(encoding="utf-8")
    needed = ["越权", "下单", "退款", "发货"]
    absent = [k for k in needed if k not in text]
    return {
        "module": "web/",
        "ok": not absent,
        "test_files": [ACCEPTANCE.name],
        "note": "等价断言缺失：" + ",".join(absent) if absent else "验收脚本覆盖写操作与越权断言",
    }


def main():
    ap = argparse.ArgumentParser(description="模块单元测试存在性门禁")
    ap.add_argument("--json", action="store_true", help="输出 JSON")
    ap.add_argument("--include-frontend", action="store_true", help="同时检查 web/ 等价断言")
    args = ap.parse_args()

    rows, missing = scan_modules()
    if args.include_frontend:
        f = scan_frontend()
        rows.append(f)
        if not f["ok"]:
            missing.append(f["module"])

    if args.json:
        print(json.dumps({"modules": rows, "missing": missing, "ok": not missing},
                         ensure_ascii=False, indent=2))
    else:
        print("模块单元测试存在性检查（internal/* 业务包）")
        print("-" * 68)
        for r in rows:
            files = ",".join(r.get("test_files", [])) or "无"
            print(f"{r['module']:24s} {'PASS' if r['ok'] else 'FAIL':5s} 测试文件：{files}")
        print("-" * 68)
        if missing:
            print("以下模块缺少单元测试，闸门 FAIL：" + "、".join(missing), file=sys.stderr)
            print("要求见 .agent/general/unit-test.md：新增模块必须先有 *_test.go 再写实现。",
                  file=sys.stderr)
        else:
            print("全部模块均有单元测试：PASS")

    return 1 if missing else 0


if __name__ == "__main__":
    sys.exit(main())
