package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/webshare-proxy/webshare-cli/internal/app"
	"github.com/webshare-proxy/webshare-cli/internal/output"
)

func newWhoamiCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the account these credentials belong to",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(cmd.Context(), flags)
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
			fmt.Fprintln(os.Stderr, output.NewStyler(os.Stderr).Dim("via "+credentialSource()))
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
			client, err := newClient(cmd.Context(), flags)
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
			client, err := newClient(cmd.Context(), flags)
			if err != nil {
				return err
			}
			account, err := app.GetAccount(cmd.Context(), client)
			if err != nil {
				return err
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, account)
			}
			profile, subscription, plan := account.Profile, account.Subscription, account.Plan
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
					[2]string{"Active plan", fmt.Sprintf("#%d %s/%s, %d proxies, %s", plan.ID, plan.ProxyType, plan.ProxySubtype, plan.ProxyCount, output.Bandwidth(plan.BandwidthLimit))},
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
