package main

import (
	"fmt"
	"strings"

	"github.com/bestfunc/tinia-cli/internal/auth"
	"github.com/spf13/cobra"
)

func newLogoutCmd() *cobra.Command {
	var host string
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "撤销本地 token（不会调用 server 端 revoke 接口）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if host == "" {
				return fmt.Errorf("缺少 --host")
			}
			host = strings.TrimRight(host, "/")
			if err := auth.Delete(host); err != nil {
				return err
			}
			fmt.Printf("✓ 已删除 %s 的本地 token\n", host)
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "要登出的 Tinia 实例 URL")
	_ = cmd.MarkFlagRequired("host")
	return cmd
}
