package main

import (
	"fmt"

	"github.com/bestfunc/tinia-cli/internal/client"
	syncpkg "github.com/bestfunc/tinia-cli/internal/sync"
	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	var nodeKey string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "确保节点已注册到内存（调 dev_reload + 列出节点）",
		Long: `节点的执行测试需要搭"流程图"才能跑（CLI 不做画布编排），所以这里仅提供：

1. 调 dev_reload 确保最新代码注册到当前用户的个人命名空间
2. 调 dev_list_nodes 列出项目下所有节点（含 namespace + key）
3. 输出 hint 让你在 Web UI 流程编辑器创建测试图运行

如果 --node 指定了 key，会高亮该节点；否则列出所有。

跑流程层面的测试，请使用 AI 工具配合 Tinia_Plugins 的 flow MCP 工具集
（flow_create / flow_batch_edit / flow_run / flow_node_output_preview）。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := client.NewFromConfig(ctx)
			if err != nil {
				return err
			}

			// 1. reload 确保最新
			fmt.Printf("→ %s (#%d) — dev_reload...\n", c.Cfg.Host, c.Cfg.ProjectID)
			if _, err := syncpkg.Reload(ctx, c); err != nil {
				return fmt.Errorf("reload: %w", err)
			}
			fmt.Println("  ✓ 已注册到当前用户的个人命名空间")

			// 2. list nodes
			var resp struct {
				Nodes []struct {
					Key      string `json:"key"`
					Name     string `json:"name"`
					Category string `json:"category"`
					Version  string `json:"version"`
				} `json:"nodes"`
			}
			if err := c.Call(ctx, "dev_list_nodes", map[string]any{"project_id": c.Cfg.ProjectID}, &resp); err != nil {
				return err
			}

			fmt.Printf("\n项目下共 %d 个节点：\n", len(resp.Nodes))
			for _, n := range resp.Nodes {
				marker := "  "
				if nodeKey != "" && n.Key == nodeKey {
					marker = "→ "
				}
				fmt.Printf("%s%s (%s) — %s, v%s\n", marker, n.Key, n.Category, n.Name, n.Version)
			}

			// 3. 输出 hint
			fmt.Printf("\n要测试节点：\n")
			fmt.Printf("  1. 浏览器打开 %s/graphs\n", c.Cfg.Host)
			fmt.Printf("  2. 新建一个分析流程，从节点面板拖入要测的节点（看 [DEV] 徽章）\n")
			fmt.Printf("  3. 连线、设参数、点运行\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&nodeKey, "node", "", "节点 key（在列表里高亮显示）")
	return cmd
}
