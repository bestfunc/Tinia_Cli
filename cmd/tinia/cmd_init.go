package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/bestfunc/tinia-cli/internal/auth"
	"github.com/bestfunc/tinia-cli/internal/client"
	"github.com/bestfunc/tinia-cli/internal/config"
	"github.com/spf13/cobra"
)

// project view（dev_list_projects 返回的子集）
type projectInfo struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Namespace   string `json:"namespace"`
	Version     string `json:"version"`
}

func newInitCmd() *cobra.Command {
	var host string
	var projectID int

	cmd := &cobra.Command{
		Use:   "init",
		Short: "在当前目录初始化 Tinia 项目（生成 .tinia/config.yaml）",
		Long: `在当前 workspace 关联到一个远端 Tinia dev project：

- 默认进入交互式选择：列出该 host 上所有可访问的 dev projects 让你选
- 或者用 --project-id 直接指定（不交互）
- 没指定 --host 时会让你从 ~/.tinia/auth.json 已登录列表里选

执行后生成 .tinia/config.yaml（含 host + project_id），可提交到 git。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			// 1. 选 host
			if host == "" {
				h, err := pickHost()
				if err != nil {
					return err
				}
				host = h
			}
			host = strings.TrimRight(host, "/")

			// 2. 取 token
			ha, err := auth.EnsureValid(ctx, host)
			if err != nil {
				return err
			}
			c := client.New(host, ha.AccessToken)

			// 3. 选 project
			var pi *projectInfo
			if projectID > 0 {
				pi, err = fetchProject(ctx, c, projectID)
				if err != nil {
					return err
				}
			} else {
				pi, err = pickProject(ctx, c)
				if err != nil {
					return err
				}
			}

			// 4. 写 .tinia/config.yaml
			cwd, _ := os.Getwd()
			cfg := &config.Config{
				Host:        host,
				ProjectID:   pi.ID,
				ProjectName: pi.Name,
				Namespace:   pi.Namespace,
			}
			if err := config.Save(cwd, cfg); err != nil {
				return err
			}

			fmt.Printf("✓ 已初始化 — host=%s project=%s (#%d, ns=%s)\n",
				host, pi.Name, pi.ID, pi.Namespace)
			fmt.Printf("  接下来可以 tinia push / tinia dev / tinia reload\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Tinia 实例 URL（不指定则从已登录列表选）")
	cmd.Flags().IntVar(&projectID, "project-id", 0, "直接指定 project ID（跳过交互选择）")
	return cmd
}

func pickHost() (string, error) {
	table, err := auth.Load()
	if err != nil {
		return "", err
	}
	if len(table) == 0 {
		return "", fmt.Errorf("没有任何登录记录，请先 tinia login --host <url>")
	}
	if len(table) == 1 {
		for h := range table {
			return h, nil
		}
	}
	hosts := []string{}
	for h := range table {
		hosts = append(hosts, h)
	}
	fmt.Println("已登录的 Tinia 实例：")
	for i, h := range hosts {
		fmt.Printf("  %d) %s\n", i+1, h)
	}
	fmt.Print("选一个 (1-", len(hosts), "): ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	idx, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || idx < 1 || idx > len(hosts) {
		return "", fmt.Errorf("无效选择")
	}
	return hosts[idx-1], nil
}

func fetchProject(ctx context.Context, c *client.Client, id int) (*projectInfo, error) {
	var resp struct {
		Project projectInfo `json:"project"`
	}
	if err := c.Call(ctx, "dev_get_project", map[string]any{"project_id": id}, &resp); err != nil {
		return nil, err
	}
	return &resp.Project, nil
}

func pickProject(ctx context.Context, c *client.Client) (*projectInfo, error) {
	var resp struct {
		Projects []projectInfo `json:"projects"`
	}
	if err := c.Call(ctx, "dev_list_projects", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Projects) == 0 {
		return nil, fmt.Errorf("当前用户没有任何 dev project，请先在 Web UI 创建（或用 dev_create_project）")
	}
	fmt.Println("可用的 dev projects：")
	for i, p := range resp.Projects {
		fmt.Printf("  %d) %s (#%d, ns=%s, v%s) %s\n",
			i+1, p.Name, p.ID, p.Namespace, p.Version, p.Description)
	}
	fmt.Print("选一个 (1-", len(resp.Projects), "): ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	idx, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || idx < 1 || idx > len(resp.Projects) {
		return nil, fmt.Errorf("无效选择")
	}
	return &resp.Projects[idx-1], nil
}
