# GitHub MCP 建仓记录（B4-01 / I-009）

> 本文件如实记录本项目的建仓方式。依据 `docs/过程记录/建仓取证.md` 的真实取证，**当时未使用 GitHub MCP 工具建仓**，故走「情况 B：教师豁免 / 补做演示」路径。本文件**不虚构任何 MCP 调用记录**。

## 用途

对应必做项 **B4-01（业务开发前 GitHub MCP 创建每人每题独立仓库）**、Issue **I-009**。当前因 MCP 工具调用留痕缺失，该项为 fail/待核定。

## 情况 B：当时未配置 GitHub MCP（真实）

### 实际建仓方式
- 建仓通过 **git + GitHub 凭据（credential manager）** 完成，未经过任何 MCP 服务器。
- GitHub REST API 只读核查返回的仓库元数据（2026-11-12 核实）：

```json
{
  "full_name": "Abeautiful-crypto/Small-Commerce-Order",
  "created_at": "2026-11-11T08:22:31Z",
  "pushed_at": "2026-11-12T02:57:58Z",
  "default_branch": "main",
  "private": false,
  "html_url": "https://github.com/Abeautiful-crypto/Small-Commerce-Order"
}
```

### 交叉取证（真实、可核验）
- `created_at` 2026-11-11T08:22:31Z（UTC）= 北京时间 **2026-11-11 16:22:31**，与 git 首提交 `3db6d6e Initial commit`（2026-11-11 16:22）**同一分钟**，可交叉印证"建仓 → 首提交"的时间对应。
- 仓库归属本账号 `Abeautiful-crypto`，公开仓库，主分支 `main`。

### 合规缺口
| 项 | 状态 |
|---|---|
| MCP 工具建仓的调用记录（工具名 / 请求 / 结果 / 时间） | **缺失**：当时 CodeBuddy 环境未配置 GitHub MCP 服务器，无法补做历史建仓动作 |
| 仓库创建时间与归属取证 | 已取得（API 元数据） |
| 建仓 → 首提交时间对应 | 已交叉印证（同分钟） |

## 可行整改路径（二选一，需教师裁定）

1. **教师豁免**：凭本文件 + `建仓取证.md` 的真实 git/GitHub 元数据，向教师申请 B4-01 豁免（标注 `teacher-exempt`）。
2. **补做 MCP 演示**：在 CodeBuddy 配置 GitHub MCP 服务器后，新建一个演示仓库/分支，导出真实的 `mcp__github__create_repository` 调用记录（工具名、请求参数、返回 html_url/created_at、时间），作为过程证据补入本文件「情况 A」。

## 自检

- [x] 若为 B：已如实记录"未用 MCP"，并附 git/GitHub 真实交叉取证（未虚构 MCP 调用）
- [ ] 若走豁免：已向教师提交本文件 + `建仓取证.md`，等待 `teacher-exempt` 标注
- [ ] 若补做演示：已导出真实 MCP 调用记录并补入「情况 A」

> 本文件不替代教师要求的 MCP 调用证据；如后续接入 MCP，将按规范在会话中留痕，并据实将 B4-01 更新为 pass。
