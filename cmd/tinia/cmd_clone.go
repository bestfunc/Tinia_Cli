package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bestfunc/tinia-cli/internal/auth"
	"github.com/bestfunc/tinia-cli/internal/client"
	"github.com/spf13/cobra"
)

func newCloneCmd() *cobra.Command {
	var host string
	var into string

	cmd := &cobra.Command{
		Use:   "clone <id-or-name>",
		Short: "把远端 dev project 拉到本地（镜像所有文件）",
		Long: `把远端 dev project 的整棵文件树镜像到本地目录。

参数可以是数字 project ID（精确）或项目名（精确匹配；多个同名让你选）。

行为：
- 默认拉到当前目录，用 --into <dir> 指定其他目录（不存在会创建）
- 目标目录必须为空（允许少量无关文件如 README / .git / .DS_Store）
- 拉完写 .tinia/config.yaml + .tinia/lastsync.json，后续直接 tinia push / dev 用

不指定 --host 时从 ~/.tinia/auth.json 已登录列表里选；只有一个时直接用。`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			idOrName := args[0]

			if host == "" {
				h, err := pickHost()
				if err != nil {
					return err
				}
				host = h
			}
			host = strings.TrimRight(host, "/")

			ha, err := auth.EnsureValid(ctx, host)
			if err != nil {
				return err
			}
			c := client.New(host, ha.AccessToken)

			pi, err := resolveProject(ctx, c, idOrName)
			if err != nil {
				return err
			}

			workspace, err := resolveCloneDir(into, pi.Name)
			if err != nil {
				return err
			}
			if err := ensureEmptyDir(workspace); err != nil {
				return err
			}

			fmt.Printf("→ 克隆 %s (#%d, ns=%s, v%s) → %s\n", pi.Name, pi.ID, pi.Namespace, pi.Version, workspace)
			if err := downloadProject(ctx, host, ha.AccessToken, workspace, pi.ID); err != nil {
				return err
			}
			if err := writeConfig(workspace, host, pi); err != nil {
				return err
			}

			fmt.Printf("\n✓ clone 完成 — host=%s project=%s (#%d)\n", host, pi.Name, pi.ID)
			fmt.Printf("  cd %s\n", relIfShorter(workspace))
			fmt.Printf("  tinia push / tinia dev / tinia reload\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Tinia 实例 URL（不指定则从已登录列表选）")
	cmd.Flags().StringVar(&into, "into", "", "目标目录（默认当前目录，传相对/绝对路径都可，不存在会创建）")
	return cmd
}

// resolveCloneDir 决定 clone 写到哪个目录：
//   - --into 不传 → 当前目录
//   - --into 是相对/绝对路径 → 创建（包括中间目录）后用
func resolveCloneDir(into, projectName string) (string, error) {
	if into == "" {
		cwd, err := os.Getwd()
		return cwd, err
	}
	abs, err := filepath.Abs(into)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return "", err
	}
	return abs, nil
}

// relIfShorter 如果相对路径比绝对路径短就用相对路径（输出更友好），
// 否则就保留绝对路径。
func relIfShorter(abs string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return abs
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		return abs
	}
	if len(rel) < len(abs) {
		return rel
	}
	return abs
}
