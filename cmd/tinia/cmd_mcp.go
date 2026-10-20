package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/bestfunc/tinia-cli/internal/auth"
	"github.com/bestfunc/tinia-cli/internal/config"
	"github.com/bestfunc/tinia-cli/internal/mcpfile"
	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "本地 MCP server（给 AI 客户端用）",
		Long: `本地 stdio MCP server，由 AI 客户端（Claude Code / Codex / opencode …）按配置启动，
不需要手动运行。远程能力走 Tinia 自己的 MCP（{host}/api/v1/mcp），这里只放必须跑在
AI 所在机器上的能力。`,
	}
	cmd.AddCommand(newMCPFileCmd())
	return cmd
}

func newMCPFileCmd() *cobra.Command {
	var host, uploadHost string
	cmd := &cobra.Command{
		Use:   "file",
		Short: "本地文件 MCP：上传本机文件到数据源 / 把 blob 拉到本机",
		Long: `stdio MCP server，提供 upload_file_to_datasource、fetch_blob、agentport_status 三个工具。

连接哪个 Tinia（依次取第一个有值的）：
  --host  →  环境变量 TINIA_ENDPOINT  →  当前目录向上找到的 .tinia/config.yaml 的 host
  →  本机桌面版 http://localhost:18720

鉴权：
  - 环境变量 TINIA_TOKEN 有值就直接用它（个人访问令牌），不走 OAuth；
  - 本机桌面版免登录；
  - 其余走 OAuth，与 tinia login 共用 ~/.tinia/auth.json。缺「数据源」权限时会
    打开浏览器补授权（申请的是已有权限的并集，不会冲掉 tinia login 拿到的权限）；
    授权链接同时打在 stderr 上，浏览器打不开时可以手动复制。

stdout 是 MCP 协议通道，所有日志都在 stderr。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// 登录流程里给人看的提示改到 stderr —— stdout 上多一行字，客户端的 JSON-RPC 解析就坏了
			auth.Out = os.Stderr

			h, source := resolveMCPHost(host)
			sess := &mcpfile.Session{
				Host:       h,
				UploadHost: strings.TrimRight(firstNonEmpty(uploadHost, os.Getenv("TINIA_UPLOAD_ENDPOINT")), "/"),
				EnvToken:   strings.TrimSpace(os.Getenv("TINIA_TOKEN")),
				HTTP:       &http.Client{}, // 不设整体超时：GB 级上传/下载要跑很久，靠请求 ctx 取消
			}
			fmt.Fprintf(os.Stderr, "[tinia mcp file] tinia %s 已启动（stdio），host=%s（来自 %s）\n", version, h, source)
			return mcpfile.NewServer(sess, version).Serve(cmd.Context(), os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Tinia 实例 URL（缺省见上）")
	cmd.Flags().StringVar(&uploadHost, "upload-host", "", "数据面直连地址，只有本地开发需要（Vite :18722 → Go :18721）；缺省读 TINIA_UPLOAD_ENDPOINT，再缺省 = host")
	return cmd
}

// resolveMCPHost 按 flag → env → 项目配置 → 本机桌面版 的顺序取 host，并说明来源（打到 stderr 方便排查）。
func resolveMCPHost(flag string) (host, source string) {
	if h := strings.TrimSpace(flag); h != "" {
		return strings.TrimRight(h, "/"), "--host"
	}
	if h := strings.TrimSpace(os.Getenv("TINIA_ENDPOINT")); h != "" {
		return strings.TrimRight(h, "/"), "TINIA_ENDPOINT"
	}
	if cfg, err := config.Load(); err == nil && cfg.Host != "" {
		return strings.TrimRight(cfg.Host, "/"), cfg.WorkspaceDir + "/.tinia/config.yaml"
	}
	return defaultDesktopHost, "默认本机桌面版"
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
