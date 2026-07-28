package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

func newActivityCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Inspect and export individual proxy requests",
	}
	cmd.AddCommand(newActivityListCmd(flags), newActivityExportCmd(flags))
	return cmd
}

func newActivityListCmd(flags *rootFlags) *cobra.Command {
	var (
		planID      int
		since       string
		errorReason string
		search      string
		limit       int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent proxy requests",
		Example: `  webshare activity list
  webshare activity list --error '*'        # only failed requests
  webshare activity list --since 15m --search example.com`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			from, err := parseSince(since)
			if err != nil {
				return err
			}
			params := webshare.ProxyActivityListParams{
				TimestampGTE: from,
				ErrorReason:  errorReason,
				Search:       search,
			}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
			}
			var activities []webshare.ProxyActivity
			for activity, err := range client.ProxyActivity.ListAll(cmd.Context(), params) {
				if err != nil {
					return err
				}
				activities = append(activities, activity)
				if limit > 0 && len(activities) >= limit {
					break
				}
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, activities)
			}
			styler := output.NewStyler(os.Stdout)
			rows := make([][]string, 0, len(activities))
			for _, a := range activities {
				target := ""
				if a.Hostname != nil {
					target = *a.Hostname
				} else if a.IPAddress != nil {
					target = *a.IPAddress
				}
				if a.Port != nil {
					target = fmt.Sprintf("%s:%d", target, *a.Port)
				}
				result := styler.Green("ok")
				if a.ErrorReason != nil {
					result = styler.Red(*a.ErrorReason)
				}
				rows = append(rows, []string{
					a.Timestamp.Local().Format("15:04:05"),
					a.Protocol,
					target,
					formatBytes(int64(a.Bytes)),
					fmt.Sprintf("%.2fs", a.RequestDuration),
					result,
				})
			}
			return output.Table(os.Stdout, []string{"TIME", "PROTO", "TARGET", "BYTES", "DURATION", "RESULT"}, rows)
		},
	}
	addPlanFlag(cmd, &planID)
	cmd.Flags().StringVar(&since, "since", "1h", "how far back to look (e.g. 15m, 24h, 7d)")
	cmd.Flags().StringVar(&errorReason, "error", "", "only requests with this error reason ('*' for any error)")
	cmd.Flags().StringVar(&search, "search", "", "search query (e.g. a hostname)")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum number of entries (0 for all)")
	return cmd
}

func newActivityExportCmd(flags *rootFlags) *cobra.Command {
	var (
		planID      int
		since       string
		errorReason string
		search      string
		outPath     string
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export proxy activity as CSV",
		Long: `Export proxy activity as the server-rendered CSV. The required download
token is fetched automatically.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			token, err := client.DownloadTokens.Get(ctx, webshare.ScopeActivity)
			if err != nil {
				return fmt.Errorf("fetching the activity download token: %w", err)
			}
			from, err := parseSince(since)
			if err != nil {
				return err
			}
			params := webshare.ProxyActivityDownloadParams{
				DownloadToken: token.Key,
				TimestampGTE:  from,
				ErrorReason:   errorReason,
				Search:        search,
			}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
			}
			csvText, err := client.ProxyActivity.Download(ctx, params)
			if err != nil {
				return err
			}
			if outPath != "" && outPath != "-" {
				return os.WriteFile(outPath, []byte(csvText), 0o644)
			}
			fmt.Print(csvText)
			return nil
		},
	}
	addPlanFlag(cmd, &planID)
	cmd.Flags().StringVar(&since, "since", "24h", "how far back to look (e.g. 15m, 24h, 7d)")
	cmd.Flags().StringVar(&errorReason, "error", "", "only requests with this error reason ('*' for any error)")
	cmd.Flags().StringVar(&search, "search", "", "search query (e.g. a hostname)")
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "write to a file instead of stdout")
	return cmd
}
