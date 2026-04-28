// 阶段 1 占位：先让命令注册进 cobra 树编译通过 + help 文案有，
// 实际逻辑在阶段 2 实装（push/pull/dev/reload/logs/run）。

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "显示当前项目的同步状态（host / project / 改动文件数）",
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("阶段 2 实装")
		},
	}
}

func newPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push",
		Short: "把当前 workspace 推到远端 dev project（增量 diff）",
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("阶段 2 实装：扫描本地文件 → diff hash → dev_write_file 推送变更 → dev_reload")
		},
	}
	cmd.Flags().Bool("dry-run", false, "只显示改动不真正推送")
	cmd.Flags().Bool("no-reload", false, "推送后不自动 reload")
	return cmd
}

func newPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "从远端 dev project 拉取文件覆盖本地（反向同步）",
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("阶段 2 实装")
		},
	}
}

func newReloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "触发远端 dev_reload（仅热加载，不上传文件）",
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("阶段 2 实装：调 dev_reload，输出注册的节点列表")
		},
	}
}

func newDevCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dev",
		Short: "watch 模式：保存文件即推送 + reload",
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("阶段 2 实装：fsnotify 监听 + debounce 200ms + 增量 push + reload")
		},
	}
}

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "查看节点运行日志",
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("阶段 2 实装：调 dev_tail_logs / 长轮询")
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "跟踪新日志（类似 tail -f）")
	return cmd
}

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "在远端跑一个测试节点 / 流程",
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("阶段 2 实装")
		},
	}
	cmd.Flags().String("node", "", "节点 key")
	return cmd
}
