# Nice Codex — Agent / AI 协作说明

## 发版与 GitHub（必读）

推送本仓库到 GitHub 涉及 **main + tag** 两步。端安装包由 **tag** 触发 Actions 构建。

**完整强制规则见：**

- [`docs/GIT_RELEASE.md`](docs/GIT_RELEASE.md)

### 最短摘要（不得跳过）

1. **先升级版本号**（`version.go` / `build/config.yml` / `frontend/package.json` / `app.ts` / README / **`update.md`**）
2. **`update.md` 写清「新增 / 修改 / 修复」**
3. **commit + `git push origin main`**
4. **annotated tag `vX.Y.Z`**，tag message 同样写清「新增 / 修改 / 修复」
5. **`git push origin vX.Y.Z`** → 触发 `.github/workflows/release.yml` 自动构建 Release

只推 main、不打/不推 tag = **没有**自动化端产物。  
轻量 tag 或空说明 tag = **不合格发版**。

---

## 其他约定

- 前端包管理：`pnpm`（勿用 npm/yarn 安装）
- 禁止无必要跑前端 `dev` / `build`（除非用户明确要求）
- 数据库变更记 `update.sql`（本桌面项目以本地配置为主时按任务范围判断）
- 用户沟通默认中文
