# 订单交易系统 · AI 开发工作流入口（AGENTS.md）

> 本文件是 AI 进入本项目后的**最高优先级行为约定**。它与 `.agent/` 下既有 12 份"宪法"文件**叠加生效、不覆盖**：原有状态机铁律、金额 `int64` 分、幂等、越权、提交规范继续有效；本文件新增的是**开发节奏的硬闸门**与**单元测试强制要求**。
>
> 依据：`.agent/README.md`、`.agent/order-dev-rules/SKILL.md`、项目现状（Go 1.22 / MySQL + Redis / `internal/{app,api,cache,model,service,store}` / `scripts/coverage_gate.py` / `scripts/acceptance_check.py`）。

## 一、AI 启动顺序（每次任务开始前）

| 顺序 | 必读文件 | 目的 |
| --- | --- | --- |
| 1 | 本文件 `AGENTS.md` | 确认开发节奏、闸门与两条新规则 |
| 2 | `.agent/README.md` | 总索引，确认本次改动属于后端 / 前端 / 通用 |
| 3 | `.agent/general/principles.md` | 状态机是唯一裁判、闭环优先、问题沉淀 |
| 4 | **`.agent/general/feature-gate.md`** | **新增规则一：逐功能点测试闸门** |
| 5 | **`.agent/general/unit-test.md`** | **新增规则二：模块单元测试强制** |
| 6 | 对应专项文件 | 后端 `backend/constraints.md` + `backend/tech-stack.md`；前端 `frontend/*.md`；提交前 `general/acceptance.md` + `general/git-convention.md` |
| 7 | `.agent/workflow/feature-points.md` | 取下一个待开发功能点，确认状态机迁移与必写单测 |

遇到任何 bug / 竞态 / 歧义：**先写 `.agent/general/issue-log.md` 再动手修**（既有规则，不因新流程改变）。

## 二、硬性工作流：一个功能点一闸门

```
取一个功能点 → 写实现 → 写/补该模块单元测试 → 跑功能点闸门
   → PASS：写证据 + 更新 progress.md + 按 git-convention 提交
   → 才允许取下一个功能点
   → FAIL：先写 issue-log → 定位 → 修复 → 原功能点重跑闸门（禁止带病前进）
```

| 规则 | 内容 |
| --- | --- |
| **G1 一次一点** | 一次只做一个功能点（如"支付通知幂等"），禁止一次铺开多个阶段 |
| **G2 完成即测** | 功能点代码写完，**立刻**跑该点闸门，不得"先写完全部再一起测" |
| **G3 测试通过才推进** | 闸门 `PASS` 是进入下一功能点 / 下一阶段的**唯一通行证**；`FAIL` 时禁止开始任何新功能点 |
| **G4 模块必有单测** | 本次改动涉及的每个模块必须有对应 `*_test.go`（或前端等价断言），缺一个即闸门 FAIL |
| **G5 证据留痕** | 闸门结果自动写入 `.agent/workflow/gate-records/<ID>.json`，作为"已测过"的唯一证据 |
| **G6 不伪造通过** | 测试没过不说过了；不得用 `--skip-deps`、改脚本、删断言、注释测试等方式绕过 |
| **G7 失败先沉淀** | 闸门失败 → 先按 `issue-log.md` 模板写一条 → 再修 → 回填"是否上升为约束" |
| **G8 范围守护** | 购物车、多商户、真实物流、部分退款、签收/退货等范围外功能一律不做；发现"顺手优化"先停下 |

> 与既有关系：`general/acceptance.md` 管**提交前**门槛，本文件的闸门管**每个功能点完成时**；前者是后者的超集，两者都过才能提交。

## 三、命令速查

| 动作 | 命令 |
| --- | --- |
| 列出全部功能点 | `python scripts/feature_gate.py --list` |
| 预演某功能点将执行什么 | `python scripts/feature_gate.py --feature F3.1 --dry-run` |
| **跑功能点闸门（必做）** | `python scripts/feature_gate.py --feature F3.1` |
| 跑整阶段闸门 | `python scripts/feature_gate.py --stage 3` |
| 检查所有模块是否有单测 | `python scripts/check_module_tests.py` |
| 覆盖率门禁（整体 / 核心包 ≥85%） | `make coverage` |
| 五大场景 HTTP 验收（需服务已启动） | `python scripts/acceptance_check.py --base http://127.0.0.1:8090` |
| 全量回归 | `go test -race -count=1 ./...` |

闸门退出码：`0 = PASS`，`1 = FAIL`，`2 = 用法/环境错误`。**只有 0 才能推进。**

## 四、功能点与进度

- 机器可读清单：`.agent/workflow/features.json`（闸门脚本读取它）
- 人读清单与逐字段解释：`.agent/workflow/feature-points.md`
- 进度追踪：`.agent/workflow/progress.md`（AI 每次闸门通过后更新状态）
- 闸门证据：`.agent/workflow/gate-records/*.json`（脚本自动生成，不要手改）

## 五、铁律速查（沿用 `.agent`，不重复定义）

1. 状态机是唯一裁判：找不到对应迁移的改动一律禁止。
2. 金额 `int64` 分、禁浮点；库存原子条件更新，禁"先查后扣"。
3. 幂等靠唯一索引 / CAS；退款恢复库存按**来源**（售前恢复、迟到不恢复）。
4. 退款 × 发货互斥靠 CAS；流水只增不改。
5. 买家接口强制 `order.Owner == currentUser`；渠道回调必须校验**真实请求身份**。
6. 提交格式 `[类型] [版本号] [编号] 描述`，中文、一类型一提交。

## 六、本文件未改动的东西

原有 `.agent/` 12 份规范内容保持原样，仅在 `.agent/README.md` 与 `.agent/order-dev-rules/SKILL.md` **末尾追加**了新规则的加载指引。业务代码、Makefile、CI、既有测试脚本均未修改；新增的两个脚本只做检查与执行，不含任何业务实现。
