package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

func newWhoamiCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the account the API key belongs to",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			profile, err := client.Profile.Get(cmd.Context())
			if err != nil {
				return err
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, profile)
			}
			fmt.Println(profile.Email)
			return nil
		},
	}
}

func newIPCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ip",
		Short: "Show your current public IP address",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			result, err := client.IPAuthorizations.WhatsMyIP(cmd.Context())
			if err != nil {
				return err
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, result)
			}
			fmt.Println(result.IPAddress)
			return nil
		},
	}
}

func newAccountCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "account",
		Short: "Show account, subscription and active plan at a glance",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			profile, err := client.Profile.Get(ctx)
			if err != nil {
				return err
			}
			subscription, err := client.Subscription.Get(ctx)
			if err != nil {
				return err
			}
			var plan *webshare.Plan
			if subscription.Plan != 0 {
				plan, err = client.Plans.Get(ctx, subscription.Plan)
				if err != nil {
					return err
				}
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, map[string]any{
					"profile":      profile,
					"subscription": subscription,
					"plan":         plan,
				})
			}
			pairs := [][2]string{
				{"Email", profile.Email},
				{"Member since", profile.CreatedAt.Format("2006-01-02")},
				{"Free credits", fmt.Sprintf("$%.2f", subscription.FreeCredits)},
				{"Term", string(subscription.Term)},
				{"Renews", subscription.EndDate.Format("2006-01-02")},
				{"Auto-renewal", strconv.FormatBool(subscription.RenewalsEnabled)},
			}
			if plan != nil {
				pairs = append(pairs,
					[2]string{"Active plan", fmt.Sprintf("#%d %s/%s, %d proxies, %s", plan.ID, plan.ProxyType, plan.ProxySubtype, plan.ProxyCount, formatBandwidth(plan.BandwidthLimit))},
				)
			}
			if subscription.Throttled {
				styler := output.NewStyler(os.Stdout)
				pairs = append(pairs, [2]string{"Throttled", styler.Red("yes")})
			}
			return output.KeyValues(os.Stdout, pairs)
		},
	}
}
