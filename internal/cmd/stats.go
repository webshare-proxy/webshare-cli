package cmd

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

// parseSince turns a duration flag ("24h", "7d") into a start timestamp.
func parseSince(since string) (*time.Time, error) {
	if since == "" {
		return nil, nil
	}
	// Accept a "d" suffix on top of time.ParseDuration units.
	if n := len(since); n > 1 && since[n-1] == 'd' {
		days, err := strconv.ParseFloat(since[:n-1], 64)
		if err == nil {
			t := time.Now().Add(-time.Duration(days * 24 * float64(time.Hour)))
			return &t, nil
		}
	}
	d, err := time.ParseDuration(since)
	if err != nil {
		return nil, usagef("invalid --since %q (examples: 90m, 24h, 7d)", since)
	}
	t := time.Now().Add(-d)
	return &t, nil
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func newStatsCmd(flags *rootFlags) *cobra.Command {
	var (
		planID int
		since  string
		hourly bool
	)
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show proxy usage statistics",
		Long: `Show aggregated proxy usage for a period (default: the last 24 hours),
or the hourly series with --hourly.`,
		Example: `  webshare stats
  webshare stats --since 7d
  webshare stats --hourly --since 48h`,
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
			params := webshare.StatsListParams{TimestampGTE: from}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
			}
			ctx := cmd.Context()
			if hourly {
				stats, err := client.Stats.List(ctx, params)
				if err != nil {
					return err
				}
				if flags.asJSON {
					return output.JSON(os.Stdout, stats)
				}
				rows := make([][]string, 0, len(stats))
				for _, s := range stats {
					rows = append(rows, []string{
						s.Timestamp.Local().Format("2006-01-02 15:04"),
						strconv.FormatInt(s.RequestsTotal, 10),
						strconv.FormatInt(s.RequestsFailed, 10),
						formatBytes(s.BandwidthTotal),
					})
				}
				return output.Table(os.Stdout, []string{"HOUR", "REQUESTS", "FAILED", "BANDWIDTH"}, rows)
			}
			stats, err := client.Stats.Aggregate(ctx, params)
			if err != nil {
				return err
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, stats)
			}
			successRate := "-"
			if stats.RequestsTotal > 0 {
				successRate = fmt.Sprintf("%.1f%%", 100*float64(stats.RequestsSuccessful)/float64(stats.RequestsTotal))
			}
			pairs := [][2]string{
				{"Requests", strconv.FormatInt(stats.RequestsTotal, 10)},
				{"Successful", fmt.Sprintf("%d (%s)", stats.RequestsSuccessful, successRate)},
				{"Failed", strconv.FormatInt(stats.RequestsFailed, 10)},
				{"Bandwidth used", formatBytes(stats.BandwidthTotal)},
				{"Bandwidth projected", formatBytes(stats.BandwidthProjected)},
				{"Unique proxies used", strconv.Itoa(stats.NumberOfProxiesUsed)},
			}
			if stats.LastRequestSentAt != nil {
				pairs = append(pairs, [2]string{"Last request", stats.LastRequestSentAt.Local().Format("2006-01-02 15:04:05")})
			}
			for _, reason := range stats.ErrorReasons {
				pairs = append(pairs, [2]string{"Error " + reason.Reason, fmt.Sprintf("%d — %s", reason.Count, reason.HowToFix)})
			}
			return output.KeyValues(os.Stdout, pairs)
		},
	}
	addPlanFlag(cmd, &planID)
	cmd.Flags().StringVar(&since, "since", "24h", "how far back to look (e.g. 90m, 24h, 7d)")
	cmd.Flags().BoolVar(&hourly, "hourly", false, "show the hourly series instead of the aggregate")
	return cmd
}
