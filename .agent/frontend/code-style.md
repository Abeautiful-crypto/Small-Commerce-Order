# 03 · 前端代码规范

> 当写页面、写组件、改交互、处理数据时遵循本文件。视觉风格见 `frontend/ui-style.md`，角色权限见 `frontend/permission.md`，后端接口契约见 `backend/constraints.md`。

## 1. 技术栈与目录（落地真实前端时）

- 当前阶段：复用 `prototype/` 下的 HTML+JS 单文件原型（便于演示与验收）。
- 若后续落地框架，推荐 **React / Vue 3 + TypeScript**，目录约定：

```
src/
  api/          # 接口封装（统一错误处理、越权 401/403 透传）
  components/   # 纯展示组件（无业务状态）
  features/     # 按角色/业务分模块（buyer/ops/warehouse/cs）
  store/        # 全局状态（单一真相源；见第 3 节）
  utils/        # money 换算、格式化、校验
  styles/       # 设计变量（见 `frontend/ui-style.md`）
```

## 2. 命名规范

- 变量/函数：`camelCase`；组件/文件：`PascalCase`（React）或 `kebab-case`（Vue SFC）。
- 状态值、角色名用常量枚举，禁止魔法字符串（`ROLE_BUYER` 而非 `"买家"` 散落各处）。
- CSS 类名：`BEM` 或 `kebab-case`，禁止无前缀的全局裸类名污染。

## 3. 状态管理（单一真相源）

- 全局业务状态（订单列表、商品可售量、当前登录角色）集中在一处 store，**禁止在多个组件里各自拷贝一份再手动同步**。
- 派生数据（如"待发货列表"）用 selector 计算，不单独存一份，避免不一致。
- 任何状态变更走统一 action/dispatcher，便于和后端状态机对齐（后端 `Transition` 守卫，前端渲染守卫见 `frontend/permission.md`）。

## 4. 金额显示（与后端 `int64` 分对齐）

- **展示层换算**：后端传来"分"，前端一律 `formatYuan(cents)` 显示 `¥100.00`，**禁止**在前端做 `price/100` 后再参与任何算术（避免浮点）。
- 任何金额比较/汇总先在"分"整数域完成，最后再格式化展示。
- 输入框若允许用户输入金额，必须校验为整数分或限定两位小数并立即转分，禁止浮点存储。

## 5. 防越权渲染（与 `frontend/permission.md` 配合）

- 即便 UI 按角色只渲染对应界面，**每个写操作函数仍必须过 `requireRole` 守卫**（见 `frontend/permission.md`）；前端隐藏按钮 ≠ 安全。
- 列表数据按 `currentUser` 过滤（买家只看自己的单），过滤逻辑放在 api 层或 store，不在渲染时"顺手 filter 假过滤"。

## 6. 禁止项（违反即打回）

- 禁止在业务代码里直接写死金额浮点运算。
- 禁止把全局状态拆成多份副本手动同步。
- 禁止用 `alert` 满天飞做反馈；统一用 toast / 内联提示组件（见 `frontend/ui-style.md`）。
- 禁止把密钥、内部接口地址写死在前端源码（用环境变量 / 配置）。
- 禁止用 `innerHTML` 拼接用户可控内容（XSS）；用受控渲染或转义。
- 禁止一次提交混多个不相关改动的前端文件（与 `general/git-convention.md` 提交规范呼应）。

## 7. 前端一页速查

1. 金额是后端"分"→ 前端只格式化展示，不做浮点算术？
2. 全局状态单一来源？派生数据用 selector？
3. 写操作过 `requireRole` 了吗？列表按当前用户过滤了吗？
4. 反馈用统一 toast，没用裸 `alert`？没用 `innerHTML` 拼用户输入？
