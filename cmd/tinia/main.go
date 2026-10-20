// Tinia CLI — 把本地代码同步到 Tinia 实例的 dev workspace。
//
// 通过 OAuth 2.1 + PKCE + 动态客户端注册（RFC 7591 DCR）跟 Tinia server 认证，
// 之后所有操作走 MCP HTTP（POST /api/v1/mcp）调用 dev_* 工具集，跟 AI 客户端
// 同一套协议。
//
// 子命令分布在 cmd/tinia/cmd_*.go 各文件里（cobra 子命令模式）。

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// version 由 ldflags 注入：go build -ldflags="-X main.version=1.0.0"
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:   "tinia",
		Short: "Tinia CLI — 把本地代码同步到 Tinia 实例的 dev workspace",
		Long: `Tinia CLI 让你在自己熟悉的编辑器（VSCode / vim / JetBrains 等）里
开发 Tinia 插件项目，通过 OAuth 一次授权后用单个命令同步到 Tinia 实例的
dev workspace 跑测试，省去打开浏览器在 Developer Studio 编辑的麻烦。

工作流（典型）：
  tinia login --host https://tinia-saas.bestfunc.com    # 一次性 OAuth 授权
  tinia list                                            # 看 host 上可用的项目
  tinia clone <id-or-name>                              # 克隆已有项目（或 tinia init <name> 新建）
  cd <project>/
  tinia push                                            # 把当前目录推到 Tinia
  tinia reload                                          # 重新装载到内存
  tinia dev                                             # watch 模式（保存即同步 + reload）
  tinia logs --follow                                   # 看节点运行日志`,
		Version:       version,
		SilenceUsage:  true, // 业务错误不刷整段 help
		SilenceErrors: true, // 自己控错误打印
	}

	root.AddCommand(
		newLoginCmd(),
		newLogoutCmd(),
		newWhoamiCmd(),
		newListCmd(),
		newInitCmd(),
		newCloneCmd(),
		newStatusCmd(),
		newPushCmd(),
		newPullCmd(),
		newReloadCmd(),
		newDevCmd(),
		newLogsCmd(),
		newRunCmd(),
		newMCPCmd(),
	)

	root.SetArgs(dispatchArgs(os.Args[0], os.Args[1:]))
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}
}

// localMCPName 随 Tinia 分发、装到用户机器上的本地 MCP 程序名（AgentPort）。
// 同一个二进制改叫这个名字时，只当 `tinia mcp` 用：
//
//	tinia-local-mcp file --host …   ≡   tinia mcp file --host …
//	tinia-local-mcp --version       照常报版本
//
// 单独起名是为了和 GUI 的 Tinia.exe、后台服务 tinia-cli.exe、开发者自己装的 tinia 命令行都不混：
// 用户在 AI 客户端配置里看到它就知道是「本地 MCP」。以 tinia 名字调用时行为完全不变。
const localMCPName = "tinia-local-mcp"

// dispatchArgs 按 argv[0] 决定交给 cobra 的参数：程序名（去掉目录与 .exe、不分大小写）
// 是 tinia-local-mcp 时，在前面补一个 "mcp"；--version / -v 留给根命令。
func dispatchArgs(argv0 string, args []string) []string {
	// 两种分隔符都切：filepath.Base 只认本平台的，交叉场景（如测试里的 Windows 路径）会漏
	name := argv0
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	if name != localMCPName {
		return args
	}
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v") {
		return args
	}
	return append([]string{"mcp"}, args...)
}
