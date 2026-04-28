# Tinia CLI

把本地代码同步到 [Tinia](https://github.com/bestfunc/Tinia) 实例的 dev workspace —— 让你在自己熟悉的编辑器（VSCode / vim / JetBrains）里开发 Tinia 插件项目，省去打开浏览器在 Developer Studio 编辑的麻烦。

## 这是什么

Tinia 是基于节点图的声学数据分析平台。开发者通常要写 Python 节点（`run.py`）+ React 视图（`Viewer.tsx`）+ `node.yaml` manifest 等多文件项目。

平台原生提供了 **Developer Studio**（浏览器内 Monaco 编辑器）能完成完整开发流程，但对于：

- 习惯本地 IDE / 想用自己的代码 snippet / linter / git workflow 的开发者
- 项目较大、文件多、需要复杂目录结构的场景
- 想用 ChatGPT / Cursor 等 AI 工具协作开发的场景

需要一个**本地编辑 → 一键同步到云端跑测试**的工具。这就是 Tinia CLI。

底层走 OAuth 2.1 + PKCE + 动态客户端注册（RFC 7591 DCR）认证，跟 [Tinia_Plugins](https://github.com/bestfunc/Tinia_Plugins) 的 MCP connector 同协议。

## 快速开始

### 安装

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/bestfunc/Tinia_Cli/latest/install.sh | sh

# 或 Homebrew（仍在筹备中）
brew install bestfunc/tap/tinia

# 或从 GitHub Releases 下载对应平台二进制
# https://github.com/bestfunc/Tinia_Cli/releases
```

也可以本地从源码编译：

```bash
git clone https://github.com/bestfunc/Tinia_Cli.git
cd Tinia_Cli
go build -o tinia ./cmd/tinia
sudo mv tinia /usr/local/bin/
```

### 典型工作流

```bash
# 一次性 OAuth 授权（按你的 Tinia 实例选 host）
tinia login --host https://tinia-saas.bestfunc.com    # SaaS 公网
tinia login --host https://t.bestfunc.com             # 公司私有化
tinia login --host http://localhost:18722             # 本地 dev

# 在你的项目目录初始化 — 选关联到哪个 dev project
cd ~/work/my-acoustic-tools
tinia init

# 把当前目录推到 Tinia
tinia push

# 改完代码，热加载
tinia reload

# 或者 watch 模式：保存即同步 + reload
tinia dev

# 看节点跑出来的日志
tinia logs --follow
```

## 命令一览

| 命令 | 说明 |
|---|---|
| `tinia login --host <url>` | OAuth 浏览器流，token 存 `~/.tinia/auth.json`（多 host 共存） |
| `tinia logout --host <url>` | 删除本地 token |
| `tinia whoami` | 列出已登录的 Tinia 实例 |
| `tinia init` | 在当前目录建 `.tinia/config.yaml`，选关联的 dev project |
| `tinia status` | 显示当前同步状态（host / project / 改动文件数） |
| `tinia push` | 增量推送（按 sha256 diff 跳过未改文件） |
| `tinia pull` | 反向拉取（远端覆盖本地） |
| `tinia reload` | 触发 `dev_reload`，把 dev project 注册到内存 |
| `tinia dev` | watch 模式：fsnotify 监听 + debounce 自动 push + reload |
| `tinia logs --follow` | 跟踪节点运行日志 |
| `tinia run --node <key>` | 测节点运行 |

## 项目配置

`tinia init` 在当前 workspace 生成 `.tinia/config.yaml`：

```yaml
host: https://tinia-saas.bestfunc.com
project_id: 42
project_name: my-acoustic-tools
namespace: bestfunc-001
# 可选：额外排除规则（默认排除 .git / node_modules / __pycache__ / .venv 等）
exclude:
  - "tmp/**"
  - "*.log"
```

**该文件可提交到 git** —— 不含敏感信息。token 单独存在 `~/.tinia/auth.json`（仅当前用户可读，永远不要提交）。

## Authentication 详情

CLI 用 OAuth 2.1 授权码 + PKCE + RFC 7591 动态客户端注册：

1. `tinia login` 调 `POST {host}/api/v1/oauth/register` 注册一次性 client
2. 起本地 `127.0.0.1:<random>/callback` 监听
3. 浏览器打开 `{host}/oauth/authorize?...&code_challenge=S256(...)`
4. 用户登录 + 同意 → 浏览器跳回 callback
5. `POST /api/v1/oauth/token` 用 code + verifier 换 access_token + refresh_token
6. 存 `~/.tinia/auth.json`（0600 权限）

access_token 默认 30 天有效，过期 CLI 自动用 refresh_token 续期；如果 refresh 也失败会提示重新 `tinia login`。

scope 默认申请 `mcp:dev mcp:nodes mcp:flow`，能调用 dev 工具集 + 节点元信息查询 + 流程执行（用于 `tinia run`）。可以 `--scopes "mcp:dev"` 缩小范围。

## 跟 Developer Studio 的区别

Tinia 浏览器内的 **Developer Studio** 是同一个 dev workspace 的 Web 视图，跟 CLI 操作的是**同一份数据**：

- CLI `push` 之后在浏览器 Developer Studio 立即能看到改动（refresh 即可）
- 浏览器里改了文件后 CLI `pull` 能拉回本地

CLI 跟 AI 工具（Claude Code 走 [Tinia_Plugins](https://github.com/bestfunc/Tinia_Plugins) 的 MCP）也是**同一套 OAuth + 同一组 dev_* 工具**，三方互不干扰，可以混合使用。

## 问题反馈

- 问题提交：https://github.com/bestfunc/Tinia_Cli/issues
- 商务合作：Great@bestfunc.com

## License

Apache-2.0
