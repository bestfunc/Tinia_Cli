package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bestfunc/tinia-cli/internal/client"
	syncpkg "github.com/bestfunc/tinia-cli/internal/sync"
	"github.com/spf13/cobra"
)

func newPushCmd() *cobra.Command {
	var dryRun bool
	var noReload bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "把当前 workspace 推到远端 dev project（增量 diff）",
		Long: `扫描当前 workspace 的所有文件 → sha256 计算 → 跟上次同步快照 + 远端文件清单 diff →
仅推送有改动的文件 + 删除已不在本地的文件，最后默认触发 dev_reload 让节点立即生效。

排除规则：
- 默认排除 .git / __pycache__ / .venv / node_modules / dist / build 等
- .tinia/ 目录始终不推
- .tinia/config.yaml 里 exclude 字段可加额外 glob

增量优化：
- 跟 .tinia/lastsync.json 对比，sha 一致 = 跳过
- 远端被人手动删了的文件会被容灾重推`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := client.NewFromConfig(ctx)
			if err != nil {
				return err
			}
			workspace := c.Cfg.WorkspaceDir

			// 1. 扫本地
			matcher := syncpkg.NewMatcher(c.Cfg.Exclude)
			locals, err := syncpkg.ScanLocal(workspace, matcher)
			if err != nil {
				return fmt.Errorf("扫描本地文件失败: %w", err)
			}

			// 2. 拉远端文件清单
			fmt.Printf("→ %s (#%d) — 拉取远端文件清单...\n", c.Cfg.Host, c.Cfg.ProjectID)
			remote, err := syncpkg.RemoteFiles(ctx, c)
			if err != nil {
				return fmt.Errorf("拉取远端文件树失败: %w", err)
			}

			// 3. 加载 lastsync
			ls, err := syncpkg.LoadLastSync(workspace)
			if err != nil {
				return err
			}

			// 4. diff
			actions := syncpkg.DiffPush(locals, remote, ls)
			if len(actions) == 0 {
				fmt.Println("✓ 无改动，本地跟远端一致")
				return nil
			}

			// 5. 预览
			uploadCnt, deleteCnt := 0, 0
			for _, a := range actions {
				if a.Op == "upload" {
					uploadCnt++
				} else {
					deleteCnt++
				}
				fmt.Printf("  %s %s\n", actionGlyph(a.Op), a.RelPath)
			}
			fmt.Printf("\n%d 个文件改动 (上传 %d / 删除 %d)\n", len(actions), uploadCnt, deleteCnt)

			if dryRun {
				fmt.Println("\n（dry-run，未真正执行）")
				return nil
			}

			// 6. 执行
			for i, a := range actions {
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
				case "delete":
					if err := syncpkg.DeleteRemoteFile(ctx, c, a.RelPath); err != nil {
						return fmt.Errorf("delete %s: %w", a.RelPath, err)
					}
					delete(ls.Files, a.RelPath)
				}
				fmt.Printf("\r  [%d/%d]", i+1, len(actions))
			}
			fmt.Println()

			// 7. 保存 lastsync
			if err := ls.Save(workspace); err != nil {
				return fmt.Errorf("保存 lastsync: %w", err)
			}

			// 8. reload
			if !noReload {
				fmt.Println("→ 触发 dev_reload...")
				out, err := syncpkg.Reload(ctx, c)
				if err != nil {
					fmt.Printf("⚠ reload 失败: %v\n", err)
				} else {
					if msg, ok := out["message"].(string); ok && msg != "" {
						fmt.Printf("  %s\n", msg)
					}
				}
			}

			fmt.Println("✓ push 完成")
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只显示改动不真正推送")
	cmd.Flags().BoolVar(&noReload, "no-reload", false, "推送后不自动 reload")
	return cmd
}

func actionGlyph(op string) string {
	switch op {
	case "upload":
		return "↑"
	case "delete":
		return "✗"
	}
	return "?"
}
