# 自编/调用 Skill 实使用证据（真实）

> 用于 B3-01/B3-03「自编业务 Skill 结构完整、真实调用；总结全部实际使用 Skill 的位置、原因、输入输出、帮助及局限」。本文件只记录可核实事实。

## 1. 自编 Skill（仓库内真实存在）

| Skill | 位置 | 状态 |
|---|---|---|
| `order-dev-rules` | `.agent/order-dev-rules/SKILL.md` | 结构完整，针对本题下单开发；缺逐次调用输入输出原文 |
| `business-rules` | 市场 Skill（被调用） | 8 条业务规则校验；`skill-usage.md` 有汇总，缺逐次调用原文 |
| `acceptance-check` | 市场 Skill（被调用） | 由 `docs/验收Agent会话.md`（E-011）完整佐证：六轮断言 48/48、硬错误 NONE、缺陷复检闭环 |

## 2. 真实使用位置与原因

- **验收阶段**（2026-11-12 11:27，`7eb94f8` 归档）：调用 `acceptance-check` 按原题五场景 + 异常 + 对账生成断言清单，作为独立验收基线。输入输出：服务地址 → 48 项断言结果。帮助：验收与缺陷回归闭环。局限：未覆盖生产只读场景（已另加 `--smoke`）。
- **开发阶段**：`.agent/order-dev-rules` 与 `business-rules` 被引用约束下单实现与规则一致性（见 `约束变更记录.md` 与 `skill-usage.md`）。

## 3. 如实缺口（须学生补证，不可伪造）

- `business-rules` / `order-dev-rules` 缺**逐次调用的输入（提示/上下文）与输出（AI 行动/文件 diff）原文**；仅有 `skill-usage.md` 汇总自述。
- 未给出「每次调用→产生了什么代码/校验」的逐条映射。

> 整改建议：从 WorkBuddy 会话导出 business-rules / order-dev-rules 的调用回合（提示 + 工具结果 + 生成文件），即可将 B3-01/B3-03 由 partial 提升至 pass。
