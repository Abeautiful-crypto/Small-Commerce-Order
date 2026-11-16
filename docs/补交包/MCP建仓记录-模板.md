# GitHub MCP 建仓记录（补交模板 · 须填真实 MCP 调用）

> ⚠️ **这是模板。若你确实通过 GitHub MCP 工具建仓，请导出真实的工具调用记录；若当时未用 MCP，请勿编造，改走「教师豁免」路径。**
> 伪造 MCP 调用 = 学术造假。

## 用途
对应必做项 **B4-01（业务开发前 GitHub MCP 创建每人每题独立仓库）**、Issue **I-009**。当前因 MCP 调用记录缺失，该项 fail。

## 情况 A：你确实用了 GitHub MCP（请填真实记录）
需包含每条调用的真实字段：
```
工具名: mcp__github__create_repository (或等价)
请求参数: { name: "Small-Commerce-Order", private: false, ... }
返回结果: { html_url, created_at, ... }
时间: 2026-11-11T...Z
会话上下文: 读取原题 assignment.md 后发起
```
落盘：`docs/补交包/MCP建仓记录.md` + 会话原文导出。

## 情况 B：当时未配置 MCP（当前真实情况，见 建仓取证.md）
- 建仓通过 `git + GitHub 凭据` 完成，`created_at` 2026-11-11T08:22:31Z 与首提交同分钟（已交叉印证）。
- **合规缺口**：缺 MCP 工具调用留痕。
- **可行路径**：在 CodeBuddy 配置 GitHub MCP 服务器后，**由教师复核或明确豁免** B4-01；或补做一次 MCP 建仓演示（新仓库/分支）作为过程证据。

## 自检
- [ ] 若为 A：记录是真实 MCP 调用回显，非手写
- [ ] 若为 B：已向教师申请豁免或补做 MCP 演示

> 处理完后告知我，我会据实更新 B4-01 状态（pass 或标注 teacher 豁免），不虚构 MCP 记录。
