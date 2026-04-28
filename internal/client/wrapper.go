// Wrapper 提供"加载 config + 自动取 token + 401 自动 refresh"的高层封装，
// 命令实现里只用 NewFromConfig() 拿现成的 client，调 Call() 失败时
// 自动重试一次（refresh token 后）。
//
// 大部分 cmd_*.go 应该用这个 wrapper，而不是直接 New()。

package client

import (
	"context"
	"errors"

	"github.com/bestfunc/tinia-cli/internal/auth"
	"github.com/bestfunc/tinia-cli/internal/config"
)

// AuthedClient 带配置 + 自动刷新 token 的 client。
type AuthedClient struct {
	*Client
	Cfg *config.Config
}

// NewFromConfig 加载当前 workspace 的 config + 取 host 对应的 token。
func NewFromConfig(ctx context.Context) (*AuthedClient, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	ha, err := auth.EnsureValid(ctx, cfg.Host)
	if err != nil {
		return nil, err
	}
	return &AuthedClient{
		Client: New(cfg.Host, ha.AccessToken),
		Cfg:    cfg,
	}, nil
}

// Call 同 Client.Call，但 401 时自动 refresh + 重试一次。
func (a *AuthedClient) Call(ctx context.Context, tool string, args any, out any) error {
	err := a.Client.Call(ctx, tool, args, out)
	var ue ErrUnauthorized
	if errors.As(err, &ue) {
		// 自动 refresh + 重试
		ha, refreshErr := auth.Refresh(ctx, a.Cfg.Host)
		if refreshErr != nil {
			return err // refresh 失败就返回原始 401
		}
		a.Client.AccessToken = ha.AccessToken
		return a.Client.Call(ctx, tool, args, out)
	}
	return err
}
