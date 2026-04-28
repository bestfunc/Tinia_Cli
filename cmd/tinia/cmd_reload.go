package main

import (
	"encoding/json"
	"fmt"

	"github.com/bestfunc/tinia-cli/internal/client"
	syncpkg "github.com/bestfunc/tinia-cli/internal/sync"
	"github.com/spf13/cobra"
)

func newReloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "触发远端 dev_reload（仅热加载，不上传文件）",
		Long: `调远端 dev_reload，把项目最新状态扫一遍并把节点注册/重载到当前用户的个人命名空间。

只热加载，不上传文件 —— 如果本地有未推送的改动需要先 tinia push。
注册是进程内的，server 重启会丢，再次调用 tinia reload 即可恢复。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := client.NewFromConfig(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("→ %s (#%d) — dev_reload...\n", c.Cfg.Host, c.Cfg.ProjectID)
			out, err := syncpkg.Reload(ctx, c)
			if err != nil {
				return err
			}
			// 通常返回 { message, registered: [...] }；尽量友好打印
			if msg, ok := out["message"].(string); ok && msg != "" {
				fmt.Printf("  %s\n", msg)
			}
			if regs, ok := out["registered"].([]any); ok && len(regs) > 0 {
				fmt.Printf("  已注册 %d 个节点：\n", len(regs))
				for _, r := range regs {
					fmt.Printf("    - %v\n", r)
				}
			} else {
				// fallback：把整个 response 打出来便于排查
				raw, _ := json.MarshalIndent(out, "  ", "  ")
				fmt.Printf("  %s\n", string(raw))
			}
			fmt.Println("✓ reload 完成")
			return nil
		},
	}
}
