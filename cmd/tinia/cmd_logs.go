package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/bestfunc/tinia-cli/internal/client"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "查看节点运行日志",
		Long: `调远端 dev_tail_logs 拿最近的节点运行日志。

注意：v1.19+ 该工具是占位状态，server 暂未持久化运行日志（详见后端
mcp_tools.go 的 dev_tail_logs 注释）。当前命令的主要用途是观察工具是否
开始返回真实数据，作为后续节点 stderr / 运行流落库的入口。

--follow（-f）：持续轮询（每 2s 一次）。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := client.NewFromConfig(ctx)
			if err != nil {
				return err
			}
			args := map[string]any{"project_id": c.Cfg.ProjectID}
			for {
				out := map[string]any{}
				if err := c.Call(ctx, "dev_tail_logs", args, &out); err != nil {
					return err
				}
				raw, _ := json.MarshalIndent(out, "", "  ")
				fmt.Println(string(raw))
				if !follow {
					return nil
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(2 * time.Second):
				}
			}
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "持续轮询新日志")
	return cmd
}
