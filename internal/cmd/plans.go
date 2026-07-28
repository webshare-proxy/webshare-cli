package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

func newPlansCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plans",
		Short: "Inspect your proxy plans",
	}
	cmd.AddCommand(newPlansListCmd(flags), newPlansShowCmd(flags))
	return cmd
}

func formatBandwidth(gb float64) string {
	if gb == 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%g GB", gb)
}

func newPlansListCmd(flags *rootFlags) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your plans (active first)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			var plans []webshare.Plan
			for plan, err := range client.Plans.ListAll(cmd.Context(), webshare.PlanListParams{}) {
				if err != nil {
					return err
				}
				if !all && plan.Status != webshare.PlanActive {
					continue
				}
				plans = append(plans, plan)
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, plans)
			}
			styler := output.NewStyler(os.Stdout)
			rows := make([][]string, 0, len(plans))
			for _, p := range plans {
				status := string(p.Status)
				if p.Status == webshare.PlanActive {
					status = styler.Green(status)
				}
				rows = append(rows, []string{
					strconv.Itoa(p.ID), status, string(p.ProxyType), string(p.ProxySubtype),
					strconv.Itoa(p.ProxyCount), formatBandwidth(p.BandwidthLimit),
					fmt.Sprintf("$%.2f/mo", p.MonthlyPrice),
				})
			}
			return output.Table(os.Stdout, []string{"ID", "STATUS", "TYPE", "SUBTYPE", "PROXIES", "BANDWIDTH", "PRICE"}, rows)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include cancelled plans")
	return cmd
}

func newPlansShowCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <plan-id>",
		Short: "Show one plan in detail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.Atoi(args[0])
			if err != nil {
				return usagef("plan ID must be a number, got %q", args[0])
			}
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			plan, err := client.Plans.Get(cmd.Context(), id)
			if err != nil {
				return err
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, plan)
			}
			pairs := [][2]string{
				{"ID", strconv.Itoa(plan.ID)},
				{"Status", string(plan.Status)},
				{"Type", fmt.Sprintf("%s/%s", plan.ProxyType, plan.ProxySubtype)},
				{"Proxies", strconv.Itoa(plan.ProxyCount)},
				{"Bandwidth", formatBandwidth(plan.BandwidthLimit)},
				{"Price", fmt.Sprintf("$%.2f/mo, $%.2f/yr", plan.MonthlyPrice, plan.YearlyPrice)},
				{"On-demand refreshes", fmt.Sprintf("%d of %d available", plan.OnDemandRefreshesAvailable, plan.OnDemandRefreshesTotal)},
				{"Replacements", fmt.Sprintf("%d of %d available", plan.ProxyReplacementsAvailable, plan.ProxyReplacementsTotal)},
				{"Sub-users", fmt.Sprintf("%d of %d available", plan.SubusersAvailable, plan.SubusersTotal)},
				{"Created", plan.CreatedAt.Format("2006-01-02")},
			}
			if len(plan.ProxyCountries) > 0 {
				countries := ""
				for code, count := range plan.ProxyCountries {
					if countries != "" {
						countries += ", "
					}
					countries += fmt.Sprintf("%s:%d", code, count)
				}
				pairs = append(pairs, [2]string{"Countries", countries})
			}
			return output.KeyValues(os.Stdout, pairs)
		},
	}
	return cmd
}
