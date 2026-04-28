package main

import (
	"fmt"

	"github.com/bestfunc/tinia-cli/internal/client"
	syncpkg "github.com/bestfunc/tinia-cli/internal/sync"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "显示当前项目的同步状态（host / project / 改动文件数）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := client.NewFromConfig(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("Host       : %s\n", c.Cfg.Host)
			fmt.Printf("Project    : %s (#%d)\n", c.Cfg.ProjectName, c.Cfg.ProjectID)
			fmt.Printf("Namespace  : %s\n", c.Cfg.Namespace)
			fmt.Printf("Workspace  : %s\n\n", c.Cfg.WorkspaceDir)

			matcher := syncpkg.NewMatcher(c.Cfg.Exclude)
			locals, err := syncpkg.ScanLocal(c.Cfg.WorkspaceDir, matcher)
			if err != nil {
				return err
			}
			remote, err := syncpkg.RemoteFiles(ctx, c)
			if err != nil {
				return err
			}
			ls, err := syncpkg.LoadLastSync(c.Cfg.WorkspaceDir)
			if err != nil {
				return err
			}
			actions := syncpkg.DiffPush(locals, remote, ls)
			if len(actions) == 0 {
				fmt.Printf("Local      : %d 个文件 / 跟远端一致 ✓\n", len(locals))
				return nil
			}
			uploadCnt, deleteCnt := 0, 0
			for _, a := range actions {
				if a.Op == "upload" {
					uploadCnt++
				} else {
					deleteCnt++
				}
			}
			fmt.Printf("Local      : %d 个文件\n", len(locals))
			fmt.Printf("Remote     : %d 个文件\n", len(remote))
			fmt.Printf("Pending    : %d 改动 (上传 %d / 删除 %d) — tinia push 同步\n",
				len(actions), uploadCnt, deleteCnt)
			return nil
		},
	}
}
