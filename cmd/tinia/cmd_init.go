package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/bestfunc/tinia-cli/internal/auth"
	"github.com/bestfunc/tinia-cli/internal/client"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var host string
	var name string
	var description string
	var template string
	var adopt bool

	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "在当前目录创建一个新的 dev project（远端 + 本地骨架）",
		Long: `在当前目录创建一个全新的 Tinia dev project：

1. 调远端 dev_create_project 创建项目（server 自动 scaffold 骨架）
2. 把 scaffold 出来的文件镜像到当前目录
3. 写 .tinia/config.yaml 关联

默认要求当前目录是空的（允许 README / LICENSE / .git / .DS_Store）。

模板（--template）：
  basic_node          最简 Python 节点骨架（默认）
  analysis_node       节点 + 自定义结果视图
  datasource_plugin   凭证 + 数据源 + 迁移 + UI 管理页
  empty               仅 tinia-repo.yaml，完全自定义

--adopt 模式（绑定已有目录）：
  当前目录已经有现成代码（如本地 git 仓库），想把它"接管"到远端继续开发。
  跳过空目录检查 + 不下载 scaffold，只在远端创建 empty 项目 + 写 .tinia/config.yaml。
  之后用 tinia push 把本地代码推上去。

不指定 --host 时从 ~/.tinia/auth.json 已登录列表里选；只有一个时直接用。

要拉取已存在的项目用 tinia clone <id-or-name>，看可用项目用 tinia list。`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			if host == "" {
				h, err := pickHost()
				if err != nil {
					return err
				}
				host = h
			}
			host = strings.TrimRight(host, "/")

			if len(args) > 0 && name == "" {
				name = args[0]
			}
			if name == "" {
				name = readLine("项目名（英文，不含空格）", "")
			}
			if name == "" {
				return fmt.Errorf("项目名必填")
			}
			// adopt 模式下 template 一律 empty —— 我们不要远端给的 scaffold 文件，
			// 用户的现有目录就是源真相。允许用户显式传别的值，但提醒他可能没意义。
			if adopt {
				if template != "" && template != "empty" {
					fmt.Printf("→ --adopt 模式下 template 强制为 empty（忽略 --template=%s，避免远端 scaffold 跟本地冲突）\n", template)
				}
				template = "empty"
			} else if template == "" {
				template = "basic_node"
			}

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if !adopt {
				if err := ensureEmptyDir(cwd); err != nil {
					return err
				}
			}

			ha, err := auth.EnsureValid(ctx, host)
			if err != nil {
				return err
			}
			c := client.New(host, ha.AccessToken)

			fmt.Printf("→ 在 %s 创建项目 %s（template=%s）...\n", host, name, template)
			pi, err := createProject(ctx, c, name, description, template)
			if err != nil {
				return err
			}
			fmt.Printf("  ✓ 远端项目已创建：#%d ns=%s\n", pi.ID, pi.Namespace)

			if !adopt {
				fmt.Println("→ 拉取 scaffold 骨架到本地...")
				if err := downloadProject(ctx, host, ha.AccessToken, cwd, pi.ID); err != nil {
					return err
				}
			}

			if err := writeConfig(cwd, host, pi); err != nil {
				return err
			}

			fmt.Printf("\n✓ init 完成 — host=%s project=%s (#%d)\n", host, pi.Name, pi.ID)
			if adopt {
				fmt.Printf("  当前目录已绑定到远端 empty 项目（未下载任何 scaffold）。\n")
				fmt.Printf("  下一步：tinia push 把本地代码推到远端\n")
			} else {
				fmt.Printf("  接下来可以编辑代码，再 tinia push / tinia dev\n")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Tinia 实例 URL（不指定则从已登录列表选）")
	cmd.Flags().StringVar(&name, "name", "", "项目名（也可作位置参数）")
	cmd.Flags().StringVar(&description, "description", "", "项目描述（可选）")
	cmd.Flags().StringVar(&template, "template", "", "模板：basic_node | analysis_node | datasource_plugin | empty（默认 basic_node；--adopt 模式下强制 empty）")
	cmd.Flags().BoolVar(&adopt, "adopt", false, "把当前已有目录绑定到一个新建的远端 empty 项目（跳过空目录检查 + 不下载 scaffold）")
	return cmd
}

// createProject 调 dev_create_project，返回新项目元数据。
func createProject(ctx context.Context, c *client.Client, name, description, template string) (*projectInfo, error) {
	args := map[string]any{
		"name":          name,
		"template_type": template,
	}
	if description != "" {
		args["description"] = description
	}
	var resp struct {
		Project projectInfo `json:"project"`
	}
	if err := c.Call(ctx, "dev_create_project", args, &resp); err != nil {
		return nil, err
	}
	if resp.Project.ID == 0 {
		return nil, fmt.Errorf("dev_create_project 返回空")
	}
	return &resp.Project, nil
}
