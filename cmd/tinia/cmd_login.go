package main

import (
	"fmt"
	"strings"

	"github.com/bestfunc/tinia-cli/internal/auth"
	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	var host string
	var scopes string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "OAuth 登录到 Tinia 实例",
		Long: `通过 OAuth 2.1 + PKCE + 动态客户端注册 (RFC 7591) 登录到指定 Tinia 实例。

示例:
  tinia login --host https://tinia-saas.bestfunc.com    # SaaS
  tinia login --host https://t.bestfunc.com             # 公司私有化
  tinia login --host http://localhost:18722             # 本地开发

成功后 token 存在 ~/.tinia/auth.json（按 host 索引，多 host 共存）。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if host == "" {
				return fmt.Errorf("缺少 --host，请指定 Tinia 实例 URL")
			}
			host = strings.TrimRight(host, "/")
			ha, err := auth.Login(cmd.Context(), host, scopes)
			if err != nil {
				return err
			}
			fmt.Printf("✓ 登录成功 — host=%s, client_id=%s, expires=%s\n",
				host, ha.ClientID, ha.ExpiresAt.Format("2006-01-02 15:04:05"))
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Tinia 实例 URL（必填）")
	cmd.Flags().StringVar(&scopes, "scopes", "", "申请的权限 scope，留空 = mcp:dev mcp:nodes mcp:flow")
	_ = cmd.MarkFlagRequired("host")
	return cmd
}
