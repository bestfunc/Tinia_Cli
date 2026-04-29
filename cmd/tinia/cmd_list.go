package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/bestfunc/tinia-cli/internal/auth"
	"github.com/bestfunc/tinia-cli/internal/client"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var host string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出当前 host 上你能访问的所有 dev project",
		Long: `列出 Tinia 实例上你能访问的 dev projects（你建的 + 你协作的）。

输出表格的 ID / NAME 可以直接传给 tinia clone <id-or-name> 拉到本地。

不指定 --host 时从 ~/.tinia/auth.json 已登录列表里选；只有一个时直接用。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

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

			projects, err := listProjects(ctx, c)
			if err != nil {
				return err
			}
			if len(projects) == 0 {
				fmt.Printf("host: %s — 没有任何 dev project\n", host)
				fmt.Println("提示：tinia init <name> 在当前目录创建一个新项目")
				return nil
			}

			fmt.Printf("host: %s — 共 %d 个 dev project\n\n", host, len(projects))
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tNAMESPACE\tVERSION\tDESCRIPTION")
			fmt.Fprintln(tw, "──\t────\t─────────\t───────\t───────────")
			for _, p := range projects {
				desc := p.Description
				if len(desc) > 50 {
					desc = desc[:47] + "..."
				}
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", p.ID, p.Name, p.Namespace, p.Version, desc)
			}
			_ = tw.Flush()

			fmt.Println("\n提示：tinia clone <id-or-name> 把项目拉到本地")
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Tinia 实例 URL（不指定则从已登录列表选）")
	return cmd
}
