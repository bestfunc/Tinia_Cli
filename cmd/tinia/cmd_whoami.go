package main

import (
	"fmt"

	"github.com/bestfunc/tinia-cli/internal/auth"
	"github.com/spf13/cobra"
)

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "列出本地已登录的 Tinia 实例",
		RunE: func(_ *cobra.Command, _ []string) error {
			table, err := auth.Load()
			if err != nil {
				return err
			}
			if len(table) == 0 {
				fmt.Println("（无登录记录，请 tinia login --host <url>）")
				return nil
			}
			fmt.Printf("%-50s %-20s %s\n", "HOST", "CLIENT_ID", "EXPIRES")
			for host, ha := range table {
				status := "valid"
				if ha.Expired() {
					status = "EXPIRED"
				}
				fmt.Printf("%-50s %-20s %s (%s)\n",
					host, ha.ClientID, ha.ExpiresAt.Format("2006-01-02 15:04"), status)
			}
			return nil
		},
	}
}
