#!/usr/bin/env python3
"""订单交易系统 · 一键验收脚本（需求文档第五节五大场景）。

对运行中的服务发起真实 HTTP 请求，逐项输出 PASS/FAIL 与实际数据，
并扫描四类硬错误：超卖 / 重复收款 / 重复释放 / 越权。
仅用 Python 标准库，无第三方依赖。

安全限制：本脚本每个场景都会调用 /api/reset 清空数据，且用无密码的演示登录，
因此只允许对 demo 环境运行；检测到 production 会直接拒绝启动，避免误清空线上数据。

用法：
    python scripts/acceptance_check.py
    python scripts/acceptance_check.py --base http://127.0.0.1:8090 --no-reset
退出码：全部断言通过为 0，否则为 1。
"""

import argparse
import json
import sys
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor

RESULTS = []
BASE = "http://127.0.0.1:8090"


def call(path, method="GET", body=None, actor=None, token=None):
    headers = {"Content-Type": "application/json; charset=utf-8"}
    if actor:
        headers["X-Role"], headers["X-User-Id"] = actor[0], actor[1]
    if token:
        headers["X-Token"] = token
    data = json.dumps(body, ensure_ascii=False).encode("utf-8") if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=20) as resp:
            return resp.status, json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read().decode("utf-8"))
        except Exception:
            return e.code, {}
    except Exception as e:  # 网络层异常也算失败断言的证据
        return -1, {"error": str(e)}


def payload(result):
    return result[1].get("data")


def check(scene, name, ok, expect, actual):
    RESULTS.append((scene, name, "PASS" if ok else "FAIL", str(expect), str(actual)))


def login(role, uid):
    return payload(call("/api/session", "POST", {"role": role, "userId": uid}))["token"]


def reset():
    call("/api/reset", "POST", {}, actor=("ops", "admin"))


def avail(pid, token):
    products = payload(call("/api/products", token=token))
    return {p["id"]: p["available"] for p in products}[pid]


def summary():
    return payload(call("/api/summary", actor=("ops", "admin")))


def order(oid):
    return payload(call("/api/orders/" + oid, actor=("ops", "admin")))


def full_state():
    return payload(call("/api/state", actor=("ops", "admin")))


def new_order(pid, qty, token):
    return call("/api/orders", "POST", {"productId": pid, "qty": qty}, token=token)


def pay(token, oid):
    intent = payload(call("/api/orders/" + oid + "/pay-intent", "POST", {}, token=token))
    status, resp = call("/api/payments/notify", "POST", intent)
    return intent, resp.get("data", {}).get("outcome", "ERROR")


def ship(oid):
    return call("/api/orders/" + oid + "/ship", "POST", {"carrier": "SF"}, actor=("warehouse", "wh"))


def refund_apply(token, oid, reason="acceptance"):
    return payload(call("/api/orders/" + oid + "/refund-apply", "POST", {"reason": reason}, token=token))


def channel_refund(rid, success):
    # 模拟渠道结果由客服触发（后端已加角色校验）
    return payload(call("/api/refunds/" + rid + "/channel-notify", "POST",
                        {"success": success}, actor=("cs", "cs")))


def review(rid, approve, note="acceptance"):
    return call("/api/refunds/" + rid + "/review", "POST",
                {"approve": approve, "note": note}, actor=("cs", "cs"))


def pending_refund():
    rows = payload(call("/api/refunds", actor=("cs", "cs")))
    return next((r for r in rows if r["status"] == "PENDING"), None)


def scene1_normal_deal(T):
    reset()
    a0 = avail("P1", T["b1"])
    o = payload(new_order("P1", 1, T["b2"]))
    check("S1", "product_visible_and_stock", a0 == 4, 4, a0)
    check("S1", "order_pending_pay", o["status"] == "PENDING_PAY", "PENDING_PAY", o["status"])
    check("S1", "price_snapshot", o["priceFen"] == 10000 and o["amountFen"] == 10000,
          "10000/10000", "%s/%s" % (o["priceFen"], o["amountFen"]))
    a1 = avail("P1", T["b1"])
    check("S1", "stock_occupied_on_order", a1 == a0 - 1, a0 - 1, a1)

    intent, outcome = pay(T["b2"], o["id"])
    check("S1", "payment_accepted", outcome == "ACCEPTED", "ACCEPTED", outcome)
    check("S1", "status_pending_ship", order(o["id"])["status"] == "PENDING_SHIP",
          "PENDING_SHIP", order(o["id"])["status"])
    recv_before = summary()["receivedFen"]
    dup = payload(call("/api/payments/notify", "POST", intent))
    check("S1", "duplicate_notify", dup["outcome"] == "DUPLICATE", "DUPLICATE", dup["outcome"])
    check("S1", "no_double_charge", summary()["receivedFen"] == recv_before,
          recv_before, summary()["receivedFen"])

    s1 = ship(o["id"])
    check("S1", "ship_once", s1[0] == 200, 200, s1[0])
    check("S1", "status_shipped", order(o["id"])["status"] == "SHIPPED",
          "SHIPPED", order(o["id"])["status"])
    s2 = ship(o["id"])
    check("S1", "ship_twice_rejected", s2[0] == 409, 409, s2[0])
    s = summary()
    check("S1", "recon_amount_and_net", s["receivedFen"] == 20000 and s["netFen"] == 20000,
          "recv=20000 net=20000", "recv=%s net=%s" % (s["receivedFen"], s["netFen"]))
    check("S1", "stock_unchanged_after_ship", avail("P1", T["b1"]) == a1, a1, avail("P1", T["b1"]))


def scene2_oversell_and_abandon(T):
    reset()
    # 用合法业务路径让预置订单退款，释放其占用的 1 件，使 P1 可售回到 5
    refund_apply(T["b1"], "NO20260001", "acceptance setup")
    r = pending_refund()
    review(r["id"], True, "setup")
    channel_refund(r["id"], True)
    a0 = avail("P1", T["b1"])
    check("S2", "setup_5_units", a0 == 5, 5, a0)

    tokens = [T["b1"], T["b2"], T["b3"]]
    with ThreadPoolExecutor(max_workers=20) as ex:
        rs = list(ex.map(lambda i: new_order("P1", 1, tokens[i % 3]), range(20)))
    ok = [r for r in rs if r[0] == 200]
    check("S2", "20_concurrent_only_5_success", len(ok) == 5, 5, len(ok))
    a1 = avail("P1", T["b1"])
    check("S2", "no_oversell_no_negative", a1 == 0, 0, a1)

    victim = ok[0][1]["data"]
    owner_token = login("buyer", victim["buyerId"])
    c1 = call("/api/orders/" + victim["id"] + "/cancel", "POST", {}, token=owner_token)
    check("S2", "cancel_release_once", c1[0] == 200 and avail("P1", T["b1"]) == 1,
          "status=200 avail=1", "status=%s avail=%s" % (c1[0], avail("P1", T["b1"])))
    c2 = call("/api/orders/" + victim["id"] + "/cancel", "POST", {}, token=owner_token)
    check("S2", "close_idempotent_no_double_release",
          c2[0] != 200 and avail("P1", T["b1"]) == 1,
          "reject + avail=1", "status=%s avail=%s" % (c2[0], avail("P1", T["b1"])))
    again = new_order("P1", 1, T["b3"])
    check("S2", "released_stock_can_be_bought",
          again[0] == 200 and avail("P1", T["b1"]) == 0,
          "status=200 avail=0", "status=%s avail=%s" % (again[0], avail("P1", T["b1"])))


def scene3_presale_refund(T):
    reset()
    # 申请期间暂停发货 + 拒绝后可继续发货
    o = payload(new_order("P2", 1, T["b2"]))
    pay(T["b2"], o["id"])
    r1 = refund_apply(T["b2"], o["id"], "reason-1")
    check("S3", "status_refunding", order(o["id"])["status"] == "REFUNDING",
          "REFUNDING", order(o["id"])["status"])
    wh = payload(call("/api/orders", actor=("warehouse", "wh")))
    check("S3", "hidden_from_warehouse", o["id"] not in [x["id"] for x in wh],
          "not in list", "in list" if o["id"] in [x["id"] for x in wh] else "not in list")
    sh = ship(o["id"])
    check("S3", "ship_blocked_during_refund", sh[0] == 409, 409, sh[0])
    review(r1["id"], False, "reject-reason")
    check("S3", "reject_back_to_pending_ship", order(o["id"])["status"] == "PENDING_SHIP",
          "PENDING_SHIP", order(o["id"])["status"])
    sh = ship(o["id"])
    check("S3", "ship_after_reject", sh[0] == 200, 200, sh[0])

    # 同意 → 渠道首次失败 → 重试成功 → 恢复库存
    o2 = payload(new_order("P2", 1, T["b2"]))
    pay(T["b2"], o2["id"])
    before = avail("P2", T["b1"])
    r2 = refund_apply(T["b2"], o2["id"], "reason-2")
    review(r2["id"], True, "approve")
    f = channel_refund(r2["id"], False)
    check("S3", "channel_fail_keeps_pending",
          f["status"] == "FAILED" and avail("P2", T["b1"]) == before,
          "FAILED avail=%s" % before, "%s avail=%s" % (f["status"], avail("P2", T["b1"])))
    call("/api/refunds/" + r2["id"] + "/retry", "POST", {}, actor=("cs", "cs"))
    channel_refund(r2["id"], True)
    check("S3", "retry_success_refunded", order(o2["id"])["status"] == "REFUNDED",
          "REFUNDED", order(o2["id"])["status"])
    check("S3", "stock_restored", avail("P2", T["b1"]) == before + 1,
          before + 1, avail("P2", T["b1"]))

    # 退款 × 发货竞态：只允许一种结果
    o3 = payload(new_order("P2", 1, T["b2"]))
    pay(T["b2"], o3["id"])
    with ThreadPoolExecutor(max_workers=2) as ex:
        f1 = ex.submit(lambda: call("/api/orders/" + o3["id"] + "/refund-apply", "POST", {}, token=T["b2"]))
        f2 = ex.submit(ship, o3["id"])
        r1s, r2s = f1.result(), f2.result()
    one = (r1s[0] == 200) != (r2s[0] == 200)
    check("S3", "refund_ship_mutex", one, "exactly one success",
          "refund=%s ship=%s" % (r1s[0], r2s[0]))
    check("S3", "final_state_single", order(o3["id"])["status"] in ("REFUNDING", "SHIPPED"),
          "REFUNDING|SHIPPED", order(o3["id"])["status"])


def scene4_late_payment(T):
    reset()
    a0 = avail("P2", T["b1"])
    o = payload(new_order("P2", 1, T["b3"]))
    call("/api/orders/" + o["id"] + "/expire", "POST", {}, token=T["b3"])
    check("S4", "order_closed", order(o["id"])["status"] == "CLOSED",
          "CLOSED", order(o["id"])["status"])
    released = avail("P2", T["b1"])
    check("S4", "release_once_on_close", released == a0, a0, released)

    intent = payload(call("/api/orders/" + o["id"] + "/pay-intent", "POST", {}, token=T["b3"]))
    late = payload(call("/api/payments/notify", "POST", intent))
    check("S4", "late_payment_to_abnormal", late["outcome"] == "LATE_PAYMENT",
          "LATE_PAYMENT", late["outcome"])
    check("S4", "no_order_revive", order(o["id"])["status"] == "ABNORMAL_PAY",
          "ABNORMAL_PAY", order(o["id"])["status"])
    check("S4", "no_stock_reoccupy", avail("P2", T["b1"]) == released,
          released, avail("P2", T["b1"]))
    abn = payload(call("/api/abnormal", actor=("cs", "cs")))
    check("S4", "abnormal_list_visible", len(abn) == 1, 1, len(abn))
    r = payload(call("/api/abnormal/" + abn[0]["id"] + "/handle", "POST", {}, actor=("cs", "cs")))
    check("S4", "refund_source_late", r["source"] == "late", "late", r["source"])
    channel_refund(r["id"], True)
    check("S4", "status_refunded", order(o["id"])["status"] == "REFUNDED",
          "REFUNDED", order(o["id"])["status"])
    check("S4", "late_refund_no_stock_restore", avail("P2", T["b1"]) == released,
          released, avail("P2", T["b1"]))
    s = summary()
    check("S4", "no_double_charge", s["receivedFen"] == 10000, 10000, s["receivedFen"])
    check("S4", "no_double_refund", s["refundedFen"] == 10000, 10000, s["refundedFen"])
    abn2 = payload(call("/api/abnormal", actor=("cs", "cs")))
    check("S4", "abnormal_record_not_erased",
          len(abn2) == 1 and abn2[0]["status"] == "REFUNDED",
          "1 record REFUNDED", "%d record %s" % (len(abn2), abn2[0]["status"] if abn2 else "-"))


def scene5_exceptions(T):
    reset()
    o = payload(new_order("P2", 1, T["b2"]))
    c = call("/api/orders/" + o["id"] + "/cancel", "POST", {}, token=T["b1"])
    check("S5", "cross_buyer_denied", c[0] == 403, 403, c[0])
    check("S5", "cross_buyer_data_unchanged", order(o["id"])["status"] == "PENDING_PAY",
          "PENDING_PAY", order(o["id"])["status"])
    c2 = call("/api/products/P1/onsale", "POST", {"onSale": False}, token=T["b1"])
    check("S5", "cross_role_denied", c2[0] == 403, 403, c2[0])
    before_prod = avail("P1", T["b1"])
    check("S5", "cross_role_data_unchanged", before_prod == 4, 4, before_prod)

    led0 = [e["note"] for e in full_state()["ledger"]]
    pay(T["b2"], o["id"])
    ship(o["id"])
    led1 = [e["note"] for e in full_state()["ledger"]]
    check("S5", "ledger_append_only",
          len(led1) > len(led0) and led1[:len(led0)] == led0,
          "prefix unchanged + growth", "before=%d after=%d prefix_ok=%s" %
          (len(led0), len(led1), led1[:len(led0)] == led0))

    # 越权探测：买家不得自助确认渠道退款成功（否则可自行批准退款并恢复库存）
    o2 = payload(new_order("P2", 1, T["b2"]))
    pay(T["b2"], o2["id"])
    r2 = refund_apply(T["b2"], o2["id"], "permission probe")
    review(r2["id"], True, "probe")
    bad = call("/api/refunds/" + r2["id"] + "/channel-notify", "POST",
               {"success": True}, token=T["b2"])
    check("S5", "buyer_cannot_confirm_channel_refund", bad[0] == 403, 403, bad[0])
    rows = payload(call("/api/refunds", actor=("cs", "cs")))
    st = next((x["status"] for x in rows if x["id"] == r2["id"]), "-")
    check("S5", "unauthorized_channel_no_state_change", st == "AWAITING", "AWAITING", st)


def report(do_reset):
    if do_reset:
        reset()
    width = [max(len(str(r[i])) for r in RESULTS + [("scene", "assertion", "result", "expect", "actual")])
             for i in range(5)]
    line = "-" * (sum(width) + 14)
    print(line)
    print("ACCEPTANCE REPORT  base=%s" % BASE)
    print(line)
    for r in RESULTS:
        print("%-4s | %-36s | %-4s | expect=%-24s | actual=%s" %
              (r[0], r[1], r[2], r[3], r[4]))
    print(line)
    fails = [r for r in RESULTS if r[2] == "FAIL"]
    hard = {
        "oversell": ["no_oversell_no_negative", "20_concurrent_only_5_success"],
        "duplicate_charge": ["no_double_charge", "duplicate_notify"],
        "duplicate_release": ["close_idempotent_no_double_release", "cancel_release_once"],
        "unauthorized": ["cross_buyer_denied", "cross_role_denied", "cross_buyer_data_unchanged",
                         "buyer_cannot_confirm_channel_refund", "unauthorized_channel_no_state_change"],
    }
    for key, names in hard.items():
        bad = [r[1] for r in fails if r[1] in names]
        print("HARD_ERROR %-20s %s" % (key, "FOUND:" + ",".join(bad) if bad else "NONE"))
    print("TOTAL=%d PASS=%d FAIL=%d" % (len(RESULTS), len(RESULTS) - len(fails), len(fails)))
    for r in fails:
        print("FAILED_ASSERT %s/%s expect=%s actual=%s" % (r[0], r[1], r[3], r[4]))
    return 1 if fails else 0


def guard_environment():
    """禁止对生产环境运行：本脚本会 reset 数据并使用无密码演示登录。"""
    status, resp = call("/api/meta")
    if status != 200:
        print("BLOCKED: cannot read /api/meta (status=%s)" % status)
        return False
    env = resp.get("data", {}).get("env")
    if env == "production":
        print("BLOCKED: target is PRODUCTION. This script resets all data and uses demo login.")
        print("Use read-only smoke checks instead (see README 'production verification').")
        return False
    if env != "demo":
        print("BLOCKED: unknown env=%r; the service must expose /api/meta.env." % env)
        return False
    return True


def smoke_checks():
    """只读冒烟验收：适用于生产环境，零写入。

    覆盖：元信息、演示登录、商品列表、订单列表（均为只读接口）。
    """
    fails = 0
    total = 0

    def sc(name, ok, expect, actual):
        nonlocal fails, total
        total += 1
        if not ok:
            fails += 1
        print("SMOKE %-18s %s expect=%s actual=%s" % (name, "PASS" if ok else "FAIL", expect, actual))

    st, meta = call("/api/meta")
    env = (meta.get("data") or {}).get("env")
    sc("meta_ok", st == 200 and bool(meta.get("data")), "200+data", st)
    sc("env_exposed", env in ("production", "demo"), "production|demo", env)

    st, sess = call("/api/session", "POST", {"role": "buyer", "userId": "u001"})
    tok = (sess.get("data") or {}).get("token")
    sc("demo_login", st == 200 and bool(tok), "200+token", st)

    st, prods = call("/api/products", token=tok)
    data = prods.get("data")
    prods_list = data if isinstance(data, list) else (data or {}).get("products") or []
    sc("products_readable", st == 200 and bool(prods_list), "200+non-empty", st)

    st, orders = call("/api/orders", token=tok)
    sc("orders_readable", st == 200, "200", st)

    print("SMOKE TOTAL=%d PASS=%d FAIL=%d" % (total, total - fails, fails))
    for r in RESULTS:
        if r[2] == "FAIL":
            print("FAILED_ASSERT %s/%s expect=%s actual=%s" % (r[0], r[1], r[3], r[4]))
    return 1 if fails else 0


def main():
    global BASE
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default=BASE)
    ap.add_argument("--no-reset", action="store_true", help="结束后不重置演示数据，保留现场")
    ap.add_argument("--smoke", action="store_true",
                    help="只读冒烟模式：可对生产环境运行，零写入（CI 部署后冒烟用）")
    args = ap.parse_args()
    BASE = args.base.rstrip("/")
    if args.smoke:
        return smoke_checks()
    if not guard_environment():
        return 2
    T = {
        "b1": login("buyer", "u001"), "b2": login("buyer", "u002"),
        "b3": login("buyer", "u003"),
    }
    scene1_normal_deal(T)
    scene2_oversell_and_abandon(T)
    scene3_presale_refund(T)
    scene4_late_payment(T)
    scene5_exceptions(T)
    return report(not args.no_reset)


if __name__ == "__main__":
    sys.exit(main())
