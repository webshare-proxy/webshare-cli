package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

func newConfigCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show and change your proxy configuration",
	}
	cmd.AddCommand(newConfigShowCmd(flags), newConfigSetCmd(flags))
	return cmd
}

func newConfigShowCmd(flags *rootFlags) *cobra.Command {
	var planID int
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the proxy configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			resolvedPlan, err := resolvePlanID(ctx, client, planID)
			if err != nil {
				return err
			}
			config, err := client.ProxyConfig.Get(ctx, resolvedPlan)
			if err != nil {
				return err
			}
			status, err := client.ProxyConfig.GetStatus(ctx, resolvedPlan)
			if err != nil {
				return err
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, map[string]any{"config": config, "status": status})
			}
			countries := make([]string, 0, len(status.Countries))
			for code, count := range status.Countries {
				countries = append(countries, fmt.Sprintf("%s:%d", code, count))
			}
			pairs := [][2]string{
				{"Plan", strconv.Itoa(resolvedPlan)},
				{"State", string(status.State)},
				{"Username", status.Username},
				{"Password", status.Password},
				{"Countries", strings.Join(countries, ", ")},
				{"Request timeout", fmt.Sprintf("%ds", config.RequestTimeout)},
				{"Idle timeout", fmt.Sprintf("%ds", config.RequestIdleTimeout)},
				{"Auto-replace invalid", strconv.FormatBool(config.AutoReplaceInvalidProxies)},
				{"Auto-replace low confidence", strconv.FormatBool(config.AutoReplaceLowCountryConfidenceProxies)},
				{"Auto-replace out of rotation", strconv.FormatBool(config.AutoReplaceOutOfRotationProxies)},
				{"Auto-replace failed site check", strconv.FormatBool(config.AutoReplaceFailedSiteCheckProxies)},
				{"Download token", config.ProxyListDownloadToken},
			}
			return output.KeyValues(os.Stdout, pairs)
		},
	}
	addPlanFlag(cmd, &planID)
	return cmd
}

func newConfigSetCmd(flags *rootFlags) *cobra.Command {
	var (
		planID      int
		username    string
		password    string
		reqTimeout  int
		idleTimeout int
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Change proxy configuration values (only the given flags change)",
		Example: `  webshare config set --password new-secret-password
  webshare config set --request-timeout 3600`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params := webshare.ProxyConfigUpdateParams{}
			changed := false
			if cmd.Flags().Changed("username") {
				params.Username = webshare.String(username)
				changed = true
			}
			if cmd.Flags().Changed("password") {
				params.Password = webshare.String(password)
				changed = true
			}
			if cmd.Flags().Changed("request-timeout") {
				params.RequestTimeout = webshare.Int(reqTimeout)
				changed = true
			}
			if cmd.Flags().Changed("idle-timeout") {
				params.RequestIdleTimeout = webshare.Int(idleTimeout)
				changed = true
			}
			if !changed {
				return usagef("nothing to change; pass --username, --password, --request-timeout or --idle-timeout")
			}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
			}
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			config, err := client.ProxyConfig.Update(cmd.Context(), params)
			if err != nil {
				return err
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, config)
			}
			fmt.Fprintln(os.Stderr, "proxy configuration updated")
			return nil
		},
	}
	addPlanFlag(cmd, &planID)
	cmd.Flags().StringVar(&username, "username", "", "proxy username (8-32 alphanumeric characters)")
	cmd.Flags().StringVar(&password, "password", "", "proxy password (8-32 alphanumeric characters)")
	cmd.Flags().IntVar(&reqTimeout, "request-timeout", 0, "maximum seconds a proxy request can be used")
	cmd.Flags().IntVar(&idleTimeout, "idle-timeout", 0, "maximum seconds a proxy request can stay idle")
	return cmd
}
