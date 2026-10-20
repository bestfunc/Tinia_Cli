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
# 一次性 OAuth 授权
tinia login                                            # 默认连本机 desktop（http://localhost:18720）
tinia login --host https://tinia-saas.bestfunc.com    # SaaS 公网
tinia login --host https://t.bestfunc.com             # 公司私有化
tinia login --host http://localhost:18722             # 本地 dev server

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
| `tinia login [--host <url>]` | OAuth 授权，token 存 `~/.tinia/auth.json`（多 host 共存）。不带 `--host` 默认 `http://localhost:18720` |
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
| `tinia mcp file [--host <url>]` | 本地文件 MCP server（stdio），由 AI 客户端启动，见下文 |

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

CLI 用 OAuth 2.1 授权码 + PKCE，按 host 类型分两条路径：

**SaaS / 私有化 / 本地 dev server**（标准 OAuth 浏览器流）：

1. `tinia login --host <url>` 调 `POST {host}/api/v1/oauth/register` 注册一次性 client（RFC 7591 DCR）
2. 起本地 `127.0.0.1:<random>/callback` 监听
3. 系统浏览器打开 `{host}/oauth/authorize?...&code_challenge=S256(...)`
4. 用户登录 + 同意 → 浏览器跳回 callback
5. `POST /api/v1/oauth/token` 用 code + verifier 换 access_token + refresh_token
6. 存 `~/.tinia/auth.json`（0600 权限）

**本机 desktop**（`tinia login` 不带 `--host`，或 host=`localhost:18720`）：

CLI 先 GET `/api/v1/meta` 检测 `edition=desktop` 后改走 `tinia://` URL scheme 唤起 Tinia.app webview 内授权 —— 避开系统浏览器跟 webview 双 session 问题（用户在 app 里已登录的 admin 直接生效，不用二次输密码）。

- 不走 DCR：hardcode 用主仓内置 client `tinia-cli-desktop`
- redirect_uri 仍走 loopback callback，daemon 端口豁免允许任意 random 端口
- 任何检测 / 唤起失败一律降级回系统浏览器流程，并打印 URL 让用户手动复制

access_token 默认 30 天有效，过期 CLI 自动用 refresh_token 续期；如果 refresh 也失败会自动重新走授权流程。

scope 默认申请 `mcp:dev mcp:nodes mcp:flow`，能调用 dev 工具集 + 节点元信息查询 + 流程执行（用于 `tinia run`）。可以 `--scopes "mcp:dev"` 缩小范围。

## 给 AI 客户端用：`tinia mcp file`

`tinia mcp file` 是一个 stdio MCP server，由 AI 客户端（Claude Code / Codex / opencode …）
按配置启动，**不需要手动运行**。它提供必须跑在 AI 所在机器上的三个工具：

| 工具 | 作用 |
|---|---|
| `upload_file_to_datasource(datasource_id, local_path, filename?)` | 把本机文件上传进 Tinia 数据源（流式，任意大小，内存不随文件变大） |
| `fetch_blob(blob_uri, save_path?, include_base64?)` | 把 Tinia 的 blob 拉到本机磁盘，返回落地路径、格式嗅探、sha256 |
| `agentport_status()` | 报告 CLI 版本和所连 Tinia 的版本，用于核对安装是否匹配 |

其余能力（流程、节点、数据源管理…）走 Tinia 自己的远程 MCP `{host}/api/v1/mcp`，两者一起配。

**连哪个 Tinia**（取第一个有值的）：`--host` → 环境变量 `TINIA_ENDPOINT` → 当前目录向上找到的
`.tinia/config.yaml` 的 `host` → 本机桌面版 `http://localhost:18720`。

**鉴权**：

- 环境变量 `TINIA_TOKEN` 有值就直接用（个人访问令牌），不走 OAuth；
- 本机桌面版（host 是 localhost 且 `/api/v1/meta` 报 `edition=desktop`）免登录；
- 其余走 OAuth，与 `tinia login` 共用 `~/.tinia/auth.json`。第一次调用缺「数据源」权限
  （`mcp:data` / `mcp:data_write`）时会打开浏览器补授权，申请的是**已有权限的并集**，
  不会把 `tinia login` 拿到的 `mcp:dev` 等冲掉。授权链接同时打在 stderr，浏览器打不开时手动复制。

stdout 是 MCP 协议通道，所有日志都在 stderr。

**配置示例**（`command` 一律写绝对路径，下面假设 CLI 装在 `~/.tinia/bin/tinia`，
Tinia 地址是 `http://10.1.2.3:18720`）：

Claude Code：

```bash
claude mcp add --scope user tinia-file -- /Users/me/.tinia/bin/tinia mcp file --host http://10.1.2.3:18720
claude mcp add --transport http --scope user tinia http://10.1.2.3:18720/api/v1/mcp
```

Codex（`~/.codex/config.toml`）：

```toml
[mcp_servers.tinia-file]
command = "/Users/me/.tinia/bin/tinia"
args = ["mcp", "file", "--host", "http://10.1.2.3:18720"]

[mcp_servers.tinia]
url = "http://10.1.2.3:18720/api/v1/mcp"
```

opencode（`~/.config/opencode/opencode.json`）：

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "tinia-file": {
      "type": "local",
      "command": ["/Users/me/.tinia/bin/tinia", "mcp", "file", "--host", "http://10.1.2.3:18720"]
    },
    "tinia": { "type": "remote", "url": "http://10.1.2.3:18720/api/v1/mcp" }
  }
}
```

Windows 下路径是 `C:\Users\me\.tinia\bin\tinia.exe`，写进 JSON / TOML 字符串时反斜杠要写成 `\\`。

> 它取代了 Node 写的 `tinia-file-mcp`（Tinia_Local_MCP 仓）：工具名、参数、返回字段不变，
> 客户机不再需要 Node。token 从 `~/.tinia/tinia-file-mcp.json` 改为与 CLI 共用 `~/.tinia/auth.json`，
> 第一次用会重新授权一次。

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
