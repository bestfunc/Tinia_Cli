// 远端文件操作：通过 MCP dev_tree / dev_read_file / dev_write_file / dev_delete_file。

package sync

import (
	"context"

	"github.com/bestfunc/tinia-cli/internal/client"
)

// treeNode 跟后端 dev_tree 返回的结构对齐
type treeNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	IsDir    bool       `json:"is_dir"`
	Children []treeNode `json:"children,omitempty"`
}

// RemoteFiles 把远端 tree 拍平成 set（仅文件，不含目录）；返回 POSIX 路径列表。
func RemoteFiles(ctx context.Context, c *client.AuthedClient) ([]string, error) {
	var resp struct {
		Tree []treeNode `json:"tree"`
	}
	if err := c.Call(ctx, "dev_tree", map[string]any{"project_id": c.Cfg.ProjectID}, &resp); err != nil {
		return nil, err
	}
	out := []string{}
	flatten(resp.Tree, &out)
	return out, nil
}

func flatten(nodes []treeNode, out *[]string) {
	for _, n := range nodes {
		if n.IsDir {
			flatten(n.Children, out)
		} else {
			*out = append(*out, n.Path)
		}
	}
}

// ReadRemoteFile 读远端单文件全内容。
func ReadRemoteFile(ctx context.Context, c *client.AuthedClient, relPath string) (string, error) {
	var resp struct {
		Content string `json:"content"`
	}
	if err := c.Call(ctx, "dev_read_file", map[string]any{
		"project_id": c.Cfg.ProjectID,
		"path":       relPath,
	}, &resp); err != nil {
		return "", err
	}
	return resp.Content, nil
}

// WriteRemoteFile 写（覆盖）远端文件。
func WriteRemoteFile(ctx context.Context, c *client.AuthedClient, relPath, content string) error {
	return c.Call(ctx, "dev_write_file", map[string]any{
		"project_id": c.Cfg.ProjectID,
		"path":       relPath,
		"content":    content,
	}, nil)
}

// DeleteRemoteFile 删除远端文件（dev_delete_file）。
func DeleteRemoteFile(ctx context.Context, c *client.AuthedClient, relPath string) error {
	return c.Call(ctx, "dev_delete_file", map[string]any{
		"project_id": c.Cfg.ProjectID,
		"path":       relPath,
	}, nil)
}

// Reload 调 dev_reload 触发热加载。
func Reload(ctx context.Context, c *client.AuthedClient) (map[string]any, error) {
	out := map[string]any{}
	if err := c.Call(ctx, "dev_reload", map[string]any{
		"project_id": c.Cfg.ProjectID,
	}, &out); err != nil {
		return nil, err
	}
	return out, nil
}
