# Nice Codex Git 发布与 Tag 规则（强制）

> **给后续 AI / 协作者的警示文档。**  
> 推送 GitHub **不等于** 发版。端侧安装包由 **push `v*` tag** 触发的 GitHub Actions 自动构建。  
> **只提交 `main`、不打 tag = 不会出 Release 产物。**

---

## 0. 铁律（必须按顺序）

```
① 升级软件版本号
② 写清 update.md 变更说明（新增 / 修改 / 修复）
③ 提交并推送 main
④ 创建带完整说明的 annotated tag（vX.Y.Z）
⑤ 推送 tag → 触发 Actions 自动构建与 Release
```

**禁止：**

- 只 `git push origin main` 就当作发版完成
- 先打 tag 再改版本号
- 用 **轻量 tag**（`git tag v1.2.7` 无 `-a` / 无说明）
- tag 说明只写一句 `release 1.2.7` 或重复 commit subject，不写用户可读变更
- 跳过 `update.md` 直接发版
- 在未推送 main 的脏工作区上打 tag

---

## 1. 升级软件版本（main 提交前）

所有版本字符串必须一致，至少同步：

| 文件 | 字段 |
|------|------|
| `version.go` | `AppVersion = "X.Y.Z"` |
| `build/config.yml` | `version: "X.Y.Z"` |
| `frontend/package.json` | `"version": "X.Y.Z"` |
| `frontend/src/stores/app.ts` | `AppVersionFallback` / `appVersion` 初始值 |
| `README.md` / `README.zh-CN.md` | 当前版本行 |
| **`update.md`** | 顶部新增 `## X.Y.Z - YYYY-MM-DD` 章节 |

版本号格式：`MAJOR.MINOR.PATCH`（如 `1.2.7`）。  
Tag 格式：**必须** `v` 前缀，如 `v1.2.7`（Actions 匹配 `v*`）。

---

## 2. 写清变更说明（`update.md` + tag message）

每次发版在 `update.md` **文件顶部**增加一节，分类写清：

```md
## X.Y.Z - YYYY-MM-DD

### 新增
- …

### 修改
- …

### 修复
- …
```

要求：

- 用**用户/测试能看懂**的中文（可带关键模块名）
- 写「做了什么、解决什么」，不要只列文件名
- 无某类变更时，该分类可省略，但至少要有一类有实质内容
- 该内容同时作为 **annotated tag 正文** 与 GitHub Release 说明的基础

---

## 3. 提交并推送 main

```bash
# 确认版本文件与 update.md 已改
git add …
git commit -m "feat: … (vX.Y.Z)"   # 或 fix: / chore: 等，subject 带版本号
git push origin main
```

确认：

```bash
git status -sb          # 应与 origin/main 同步，无未提交发版改动
git log -1 --oneline    # 最新提交即本版
```

---

## 4. 创建 annotated tag（关键）

**必须在 main 最新发版提交上**打 **annotated** tag，message 必须包含「新增 / 修改 / 修复」：

```bash
# Windows PowerShell 注意：# 在双引号字符串里会当注释，优先用 [新增] 或 UTF-8 文件写入
$msg = @"
Nice Codex v1.2.7

[新增]
- 模型竞技场分栏：2–8 栏并排、同厂商多栏、拖拽换位
- 每栏独立会话；快捷会话按项目分组 + 图标 + Tooltip

[修改]
- 会话下拉限宽与长文本省略
- 去掉侧边栏右键提示常驻文案

[修复]
- 各栏时间线互不抢焦点；Grok 非活动会话可按栏展示
- 同厂商多栏禁止绑定同一会话
"@
[System.IO.File]::WriteAllText(
  "$PWD\.git\TAG_MSG_v1.2.7.txt",
  $msg.Trim() + "`n",
  [System.Text.UTF8Encoding]::new($false)
)
git tag -a v1.2.7 -F .git/TAG_MSG_v1.2.7.txt
```

`update.md` 里仍可用 Markdown 的 `### 新增` 分类；**tag message 在 PowerShell 下建议用 `[新增]` / `[修改]` / `[修复]`**，避免编码与注释坑。

校验：

```bash
git show v1.2.7 --no-patch
# 应看到 Tagger + 完整中文说明，而不是只有 commit 一行
```

---

## 5. 推送 tag（触发 Actions）

```bash
git push origin v1.2.7
# 或：git push origin refs/tags/v1.2.7
```

推送后：

1. GitHub Actions 工作流 `.github/workflows/release.yml` 因 `on.push.tags: v*` 启动  
2. 构建 Windows / macOS 产物  
3. 创建 GitHub Release（`Nice Codex vX.Y.Z`）并上传安装包  

**没有这一步，就不会有自动化端产物。**

---

## 6. 为什么 tag 说明必须写清楚？

| 受众 | 需要什么 |
|------|----------|
| 用户 / 测试 | 这个安装包相对上一版改了什么 |
| Actions Release 页 | 可读的版本说明（不要只靠乱序 commit 列表） |
| 后续 AI | 对照 `update.md` 与历史 tag，避免漏发版步骤 |
| 排错 | 某次端构建对应哪组功能/修复 |

仅 `generate_release_notes` 生成的 commit 列表**不够**：必须有人工整理的「新增 / 修改 / 修复」。

---

## 7. 检查清单（发版 PR / 任务结束前）

- [ ] 版本号已同步到全部版本文件  
- [ ] `update.md` 顶部已有本版「新增 / 修改 / 修复」  
- [ ] `main` 已 commit 且 `git push origin main` 成功  
- [ ] 本地 `git status` 干净（或仅剩无关本地文件）  
- [ ] 已创建 **annotated** tag `vX.Y.Z`，正文含三类说明  
- [ ] 已 `git push origin vX.Y.Z`  
- [ ] 已在 GitHub Actions 确认 Release workflow 已排队/运行  
- [ ] （可选）打开 Releases 页确认资产与说明正确  

---

## 8. 禁止的错误示例

```bash
# ❌ 只推 main
git push origin main

# ❌ 轻量 tag，无说明
git tag v1.2.7
git push origin v1.2.7

# ❌ 说明太空
git tag -a v1.2.7 -m "release"

# ❌ 版本还是旧的就打 tag
# version.go 仍是 1.2.6 却 tag v1.2.7
```

正确最小闭环：

```bash
# 1) 改版本 + update.md → commit → push main
# 2) annotated tag + 完整说明
# 3) push tag
git push origin main
git tag -a v1.2.7 -F path/to/tag-message.txt
git push origin v1.2.7
```

---

## 9. 与本仓库 Actions 的对应关系

- 工作流：`.github/workflows/release.yml`  
- 触发：`push` tags `v*`，或 `workflow_dispatch` 手动指定 tag  
- 产物：Windows `.exe`、macOS `.zip` 等上传到 GitHub Release  

因此：**main = 源码线；tag = 发版线。** 两边都要推，tag 说明要当「端版本说明书」来写。

---

*文档维护：每次调整发版流程时同步更新本文；AI 执行「提交 GitHub / 发版」任务时必须先读本文。*
