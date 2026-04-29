// 项目相关公共逻辑：选 host / 列项目 / 解析 id-or-name / 下载文件树 / 目录守卫。
// 给 list / clone / init 几个命令复用。

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bestfunc/tinia-cli/internal/auth"
	"github.com/bestfunc/tinia-cli/internal/client"
	"github.com/bestfunc/tinia-cli/internal/config"
	syncpkg "github.com/bestfunc/tinia-cli/internal/sync"
)

// projectInfo dev_list_projects / dev_get_project / dev_create_project 返回的子集。
type projectInfo struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Namespace   string `json:"namespace"`
	Version     string `json:"version"`
}

// pickHost 从 ~/.tinia/auth.json 已登录列表里选 host。
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
	hosts := make([]string, 0, len(table))
	for h := range table {
		hosts = append(hosts, h)
	}
	fmt.Println("已登录的 Tinia 实例：")
	for i, h := range hosts {
		fmt.Printf("  %d) %s\n", i+1, h)
	}
	idx := readChoice(len(hosts))
	if idx < 0 {
		return "", fmt.Errorf("无效选择")
	}
	return hosts[idx], nil
}

// fetchProject 调 dev_get_project 拿单个项目详情。
func fetchProject(ctx context.Context, c *client.Client, id int) (*projectInfo, error) {
	var resp struct {
		Project projectInfo `json:"project"`
	}
	if err := c.Call(ctx, "dev_get_project", map[string]any{"project_id": id}, &resp); err != nil {
		return nil, err
	}
	return &resp.Project, nil
}

// listProjects 调 dev_list_projects（裸 client 即可，不需要 project context）。
func listProjects(ctx context.Context, c *client.Client) ([]projectInfo, error) {
	var resp struct {
		Projects []projectInfo `json:"projects"`
	}
	if err := c.Call(ctx, "dev_list_projects", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return resp.Projects, nil
}

// resolveProject 把用户输入（数字 id 或名字）解析成 projectInfo。
// 数字 → dev_get_project；非数字 → list 后按 name 精确匹配（多匹配交互选）。
func resolveProject(ctx context.Context, c *client.Client, idOrName string) (*projectInfo, error) {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return nil, fmt.Errorf("缺少 project id 或 name")
	}
	if id, err := strconv.Atoi(idOrName); err == nil {
		return fetchProject(ctx, c, id)
	}

	all, err := listProjects(ctx, c)
	if err != nil {
		return nil, err
	}
	matches := make([]projectInfo, 0, 4)
	for _, p := range all {
		if p.Name == idOrName {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("没有找到名字为 %q 的项目（用 tinia list 看可用项目）", idOrName)
	case 1:
		return &matches[0], nil
	default:
		fmt.Printf("有 %d 个同名项目，选一个：\n", len(matches))
		for i, p := range matches {
			fmt.Printf("  %d) #%d ns=%s v%s — %s\n", i+1, p.ID, p.Namespace, p.Version, p.Description)
		}
		idx := readChoice(len(matches))
		if idx < 0 {
			return nil, fmt.Errorf("无效选择")
		}
		return &matches[idx], nil
	}
}

// downloadProject 把远端项目所有文件镜像到 workspace，并写 lastsync.json。
// 需要传 host + projectID（构造临时 AuthedClient 给 sync 包用）。
// 不写 config.yaml —— 由调用者决定。
func downloadProject(ctx context.Context, host string, token string, workspace string, projectID int) error {
	c := &client.AuthedClient{
		Client: client.New(host, token),
		Cfg: &config.Config{
			Host:      strings.TrimRight(host, "/"),
			ProjectID: projectID,
		},
	}

	remote, err := syncpkg.RemoteFiles(ctx, c)
	if err != nil {
		return fmt.Errorf("拉取远端文件清单: %w", err)
	}

	ls := &syncpkg.LastSync{Files: map[string]string{}}
	if len(remote) == 0 {
		fmt.Println("  （远端为空，没文件可下载）")
		return ls.Save(workspace)
	}

	for i, relPath := range remote {
		content, err := syncpkg.ReadRemoteFile(ctx, c, relPath)
		if err != nil {
			return fmt.Errorf("读 %s: %w", relPath, err)
		}
		full := filepath.Join(workspace, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			return fmt.Errorf("写 %s: %w", relPath, err)
		}
		h := sha256.Sum256([]byte(content))
		ls.Files[relPath] = hex.EncodeToString(h[:])
		fmt.Printf("\r  [%d/%d] %s", i+1, len(remote), relPath)
	}
	fmt.Println()
	return ls.Save(workspace)
}

// ensureEmptyDir 检查目录是否"足够空"以接收 clone / init —— 允许少量
// 无关文件（README / LICENSE / .git / .DS_Store），其他存在视为不安全，中止。
func ensureEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	allowed := map[string]bool{
		"README.md": true, "README": true, "LICENSE": true, "LICENSE.md": true,
		".git": true, ".gitignore": true, ".DS_Store": true,
	}
	bad := []string{}
	for _, e := range entries {
		if !allowed[e.Name()] {
			bad = append(bad, e.Name())
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("当前目录非空（包含 %s 等），请换一个空目录或先清理", strings.Join(bad, ", "))
	}
	return nil
}

// writeConfig 包装 config.Save，让 init / clone 写配置更简洁。
func writeConfig(workspace, host string, p *projectInfo) error {
	cfg := &config.Config{
		Host:        strings.TrimRight(host, "/"),
		ProjectID:   p.ID,
		ProjectName: p.Name,
		Namespace:   p.Namespace,
	}
	return config.Save(workspace, cfg)
}

// readChoice 读 stdin 一个 1..max 的数字，越界 / 解析失败返回 -1。
func readChoice(max int) int {
	fmt.Printf("选一个 (1-%d): ", max)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	idx, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || idx < 1 || idx > max {
		return -1
	}
	return idx - 1
}

// readLine 读 stdin 一行（trim 空白），prompt 不为空时打印。
// 主要给 init 交互问 name / namespace / description 用。
func readLine(prompt, defaultVal string) string {
	if prompt != "" {
		if defaultVal != "" {
			fmt.Printf("%s [%s]: ", prompt, defaultVal)
		} else {
			fmt.Printf("%s: ", prompt)
		}
	}
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	v := strings.TrimSpace(line)
	if v == "" {
		return defaultVal
	}
	return v
}
