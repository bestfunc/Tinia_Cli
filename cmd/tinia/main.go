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
  cd my-plugin/
  tinia login --host https://tinia-saas.bestfunc.com    # 一次性 OAuth 授权
  tinia init                                            # 选 / 创建 dev project
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
		newInitCmd(),
		newStatusCmd(),
		newPushCmd(),
		newPullCmd(),
		newReloadCmd(),
		newDevCmd(),
		newLogsCmd(),
		newRunCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}
}
