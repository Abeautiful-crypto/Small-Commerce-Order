# AI 使用说明（提示词协同记录）

> 本项目按题目要求在开发/测试/验收/部署各阶段使用 AI 协同，全部提示词原文存档于
> `C:\Users\91526\WorkBuddy\2026-11-11-15-46-42\user-prompts-all-rounds.md`（多轮完整导出），
> 单轮记录见同目录 `prompts-this-round.md`。本文件说明各阶段 AI 的使用方式与关键提示词索引。

## 各阶段 AI 使用方式

| 阶段 | 使用方式 | 关键产出 |
|---|---|---|
| 需求分析与任务规划 | 用 AI 拆解 8 条业务规则与 7 态状态机，生成约束文档骨架 | `.agent/` 全套约束文件 |
| 后端开发 | 按规则逐条实现，AI 负责存储抽象/SQL 条件更新/CAS 设计 | `internal/store`、`internal/service` |
| 测试 | AI 生成五大场景断言与并发测试；v1.2.0 补齐单测（miniredis/sqlmock） | 各包 `*_test.go`、`docs/测试报告.md` |
| 验收 | AI 编写一键 HTTP 验收脚本并修复越权缺陷 | `scripts/acceptance_check.py`、issue-log |
| 部署 | AI 生成 Dockerfile/compose/1Panel 配置与上线脚本 | `deploy/`、`docker-compose.yml` |
| 整改（v1.2.0） | 用户下达审计整改指令，AI 按清单执行并逐步交付 | 缓存修复、覆盖率 ≥85%、CI、Git 协同 |

## v1.2.0 整改会话关键提示词（原文节选）

1. 「@command://student-project-audit 来对这个项目进行评分吧，这个是ai提示语 …」
   —— 触发单题检查，产出审计报告（6.68/10 不合格 + 整改清单）。
2. 「请根据这些问题来进行优化，希望下次能达到及格分以上。」
   —— 触发整改执行：缺陷修复 → 覆盖率 → CI → Git 协同 → 过程产物。
3. 「请继续」—— 分阶段推进直至整改清单完成。

## 协同纪律

- 提交遵循 `.agent/general/git-convention.md`（类型/版本号/编号；未接入 meegle 已注明）。
- 真实密钥不进提示词、不进仓库（见 README「凭据纪律」）。
- AI 产出一律经测试/验收/覆盖率门禁后才合并（main 分支保护要求 CI `test` 检查通过）。
