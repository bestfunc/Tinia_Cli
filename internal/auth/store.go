// Token 持久化在 ~/.tinia/auth.json，格式如下：
//
//	{
//	  "https://tinia-saas.bestfunc.com": {
//	    "client_id":     "abc123",
//	    "access_token":  "...",
//	    "refresh_token": "...",
//	    "expires_at":    "2026-05-28T...",
//	    "scopes":        ["mcp:dev", "mcp:nodes", "mcp:flow"]
//	  },
//	  "https://t.bestfunc.com": { ... }
//	}
//
// 多 host 共存：同一台机器可同时连 SaaS / 公司私有化 / 本地 dev。
// 文件权限 0600（仅当前用户可读），跟 ~/.ssh/id_rsa 同级。

package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// HostAuth 单 host 的认证信息。
type HostAuth struct {
	ClientID     string    `json:"client_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	// Scopes server 实际授予的 scope。老版本 CLI 写的 auth.json 没有这个字段。
	Scopes []string `json:"scopes,omitempty"`
}

// ScopeList 返回已授予的 scope。老 auth.json 没记 scope 时，按 tinia login 的默认
// 申请值算 —— 那是老版本唯一会写这个文件的途径。
func (h *HostAuth) ScopeList() []string {
	if len(h.Scopes) == 0 {
		return splitScopes(defaultScopes)
	}
	return h.Scopes
}

// HasScopes 是否带齐 need 里的每个 scope。
func (h *HostAuth) HasScopes(need []string) bool {
	return len(missingScopes(h.ScopeList(), need)) == 0
}

// Expired 检查 access_token 是否已过期（保守策略：剩 60 秒就当过期，提前刷新）。
func (h *HostAuth) Expired() bool {
	return time.Now().Add(60 * time.Second).After(h.ExpiresAt)
}

// authFile = ~/.tinia/auth.json
func authFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".tinia")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "auth.json"), nil
}

// Load 读全表。文件不存在返回空 map（不是错误）。
func Load() (map[string]*HostAuth, error) {
	path, err := authFile()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]*HostAuth{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]*HostAuth{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("auth.json 损坏: %w", err)
	}
	return out, nil
}

// Save 写回全表（原子：先写 .tmp 再 rename）。
func Save(table map[string]*HostAuth) error {
	path, err := authFile()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(table, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Get 取指定 host 的认证；不存在返回 nil。
func Get(host string) (*HostAuth, error) {
	table, err := Load()
	if err != nil {
		return nil, err
	}
	return table[host], nil
}

// Put 存指定 host 的认证（覆盖）。
func Put(host string, ha *HostAuth) error {
	table, err := Load()
	if err != nil {
		return err
	}
	table[host] = ha
	return Save(table)
}

// Delete 删指定 host 的认证（logout 用）。
func Delete(host string) error {
	table, err := Load()
	if err != nil {
		return err
	}
	delete(table, host)
	return Save(table)
}
