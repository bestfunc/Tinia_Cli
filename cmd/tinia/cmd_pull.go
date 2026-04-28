package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bestfunc/tinia-cli/internal/client"
	syncpkg "github.com/bestfunc/tinia-cli/internal/sync"
	"github.com/spf13/cobra"
)

func newPullCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "从远端 dev project 拉取文件覆盖本地",
		Long: `把远端 dev workspace 的文件镜像到本地（远端 → 本地，覆盖式）。

危险提示：
- 本地相对远端的"额外文件"不会被删（避免误删 git 工作区文件）
- 但远端有的文件会**直接覆盖**本地同名文件（不询问）
- 强烈建议执行前先 git stash 或 commit 当前未保存的工作

完成后更新 .tinia/lastsync.json，下次 tinia push 把本地变更推回去时增量正确。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := client.NewFromConfig(ctx)
			if err != nil {
				return err
			}
			workspace := c.Cfg.WorkspaceDir

			fmt.Printf("→ %s (#%d) — 拉取远端文件清单...\n", c.Cfg.Host, c.Cfg.ProjectID)
			remote, err := syncpkg.RemoteFiles(ctx, c)
			if err != nil {
				return err
			}
			if len(remote) == 0 {
				fmt.Println("（远端为空，没什么可拉的）")
				return nil
			}

			ls, err := syncpkg.LoadLastSync(workspace)
			if err != nil {
				return err
			}

			fmt.Printf("\n远端有 %d 个文件，将下载到本地（覆盖同名）：\n", len(remote))
			for _, p := range remote {
				fmt.Printf("  ↓ %s\n", p)
			}
			if dryRun {
				fmt.Println("\n（dry-run，未真正执行）")
				return nil
			}

			for i, relPath := range remote {
				content, err := syncpkg.ReadRemoteFile(ctx, c, relPath)
				if err != nil {
					return fmt.Errorf("read %s: %w", relPath, err)
				}
				full := filepath.Join(workspace, filepath.FromSlash(relPath))
				if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
					return err
				}
				if err := os.WriteFile(full, []byte(content), 0644); err != nil {
					return fmt.Errorf("write %s: %w", relPath, err)
				}
				h := sha256.Sum256([]byte(content))
				ls.Files[relPath] = hex.EncodeToString(h[:])
				fmt.Printf("\r  [%d/%d]", i+1, len(remote))
			}
			fmt.Println()

			if err := ls.Save(workspace); err != nil {
				return err
			}
			fmt.Printf("✓ pull 完成 — %d 个文件\n", len(remote))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只显示要拉的文件不真正下载")
	return cmd
}
