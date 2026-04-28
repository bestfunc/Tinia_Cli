package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bestfunc/tinia-cli/internal/client"
	syncpkg "github.com/bestfunc/tinia-cli/internal/sync"
	"github.com/bestfunc/tinia-cli/internal/watch"
	"github.com/spf13/cobra"
)

func newDevCmd() *cobra.Command {
	var debounceMs int
	var noReload bool
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "watch 模式：保存文件即推送 + reload",
		Long: `进入 watch 模式 — 监听 workspace 文件变化（fsnotify），debounce 后自动增量 push + reload。

跟 tinia push 共用 .tinia/lastsync.json，所以中途 Ctrl+C 之后再单跑 push / dev 都不会丢状态。

按 Ctrl+C 退出。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := client.NewFromConfig(ctx)
			if err != nil {
				return err
			}
			workspace := c.Cfg.WorkspaceDir
			matcher := syncpkg.NewMatcher(c.Cfg.Exclude)

			fmt.Printf("→ %s (#%d) — watch %s\n", c.Cfg.Host, c.Cfg.ProjectID, workspace)
			fmt.Printf("  按 Ctrl+C 退出\n\n")

			// 入场先做一次完整 sync，跟 tinia push 一致
			if err := devSync(ctx, c, workspace, matcher, !noReload); err != nil {
				fmt.Printf("⚠ 初始 sync 失败: %v\n", err)
			}

			// 进 watch 循环
			return watch.Run(ctx, workspace, matcher, time.Duration(debounceMs)*time.Millisecond, func(_ []string) error {
				ts := time.Now().Format("15:04:05")
				fmt.Printf("[%s] 文件变化，同步中...\n", ts)
				if err := devSync(ctx, c, workspace, matcher, !noReload); err != nil {
					fmt.Printf("⚠ 同步失败: %v\n", err)
				}
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&debounceMs, "debounce", 200, "文件变化 debounce 时间（毫秒）")
	cmd.Flags().BoolVar(&noReload, "no-reload", false, "推送后不自动 reload")
	return cmd
}

// devSync 一次完整的 scan + diff + push + reload，被 watch 循环复用。
func devSync(ctx context.Context, c *client.AuthedClient, workspace string, matcher *syncpkg.Matcher, reload bool) error {
	locals, err := syncpkg.ScanLocal(workspace, matcher)
	if err != nil {
		return err
	}
	remote, err := syncpkg.RemoteFiles(ctx, c)
	if err != nil {
		return err
	}
	ls, err := syncpkg.LoadLastSync(workspace)
	if err != nil {
		return err
	}
	actions := syncpkg.DiffPush(locals, remote, ls)
	if len(actions) == 0 {
		fmt.Println("  ✓ 无改动")
		return nil
	}
	for _, a := range actions {
		switch a.Op {
		case "upload":
			full := filepath.Join(workspace, filepath.FromSlash(a.RelPath))
			data, err := os.ReadFile(full)
			if err != nil {
				return fmt.Errorf("读 %s: %w", a.RelPath, err)
			}
			if err := syncpkg.WriteRemoteFile(ctx, c, a.RelPath, string(data)); err != nil {
				return fmt.Errorf("upload %s: %w", a.RelPath, err)
			}
			ls.Files[a.RelPath] = a.SHA256
			fmt.Printf("  ↑ %s\n", a.RelPath)
		case "delete":
			if err := syncpkg.DeleteRemoteFile(ctx, c, a.RelPath); err != nil {
				return fmt.Errorf("delete %s: %w", a.RelPath, err)
			}
			delete(ls.Files, a.RelPath)
			fmt.Printf("  ✗ %s\n", a.RelPath)
		}
	}
	if err := ls.Save(workspace); err != nil {
		return err
	}
	if reload {
		if _, err := syncpkg.Reload(ctx, c); err != nil {
			return fmt.Errorf("reload: %w", err)
		}
		fmt.Println("  ⟳ reload OK")
	}
	return nil
}
