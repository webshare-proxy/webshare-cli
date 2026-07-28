package cmd

import (
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

func newIPAuthCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ipauth",
		Short: "Manage IP authorizations for credential-less proxy use",
	}
	cmd.AddCommand(newIPAuthListCmd(flags), newIPAuthAddCmd(flags), newIPAuthRemoveCmd(flags))
	return cmd
}

func newIPAuthListCmd(flags *rootFlags) *cobra.Command {
	var planID int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List authorized IP addresses",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			params := webshare.IPAuthorizationListParams{}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
			}
			var auths []webshare.IPAuthorization
			for auth, err := range client.IPAuthorizations.ListAll(cmd.Context(), params) {
				if err != nil {
					return err
				}
				auths = append(auths, auth)
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, auths)
			}
			rows := make([][]string, 0, len(auths))
			for _, a := range auths {
				lastUsed := "never"
				if a.LastUsedAt != nil {
					lastUsed = a.LastUsedAt.Local().Format("2006-01-02 15:04")
				}
				rows = append(rows, []string{
					strconv.Itoa(a.ID), a.IPAddress,
					a.CreatedAt.Local().Format("2006-01-02"), lastUsed,
				})
			}
			return output.Table(os.Stdout, []string{"ID", "IP", "ADDED", "LAST USED"}, rows)
		},
	}
	addPlanFlag(cmd, &planID)
	return cmd
}

func newIPAuthAddCmd(flags *rootFlags) *cobra.Command {
	var (
		planID  int
		current bool
	)
	cmd := &cobra.Command{
		Use:   "add [ip-address]",
		Short: "Authorize an IP address",
		Example: `  webshare ipauth add 203.0.113.7
  webshare ipauth add --current   # authorize this machine's public IP`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if current == (len(args) == 1) {
				return usagef("pass exactly one of an IP address or --current")
			}
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			ip := ""
			if current {
				result, err := client.IPAuthorizations.WhatsMyIP(ctx)
				if err != nil {
					return fmt.Errorf("detecting your public IP: %w", err)
				}
				ip = result.IPAddress
			} else {
				ip = args[0]
				if net.ParseIP(ip) == nil {
					return usagef("%q is not a valid IP address", ip)
				}
			}
			params := webshare.IPAuthorizationCreateParams{IPAddress: ip}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
			}
			auth, err := client.IPAuthorizations.Create(ctx, params)
			if err != nil {
				return err
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, auth)
			}
			fmt.Printf("authorized %s (id %d)\n", auth.IPAddress, auth.ID)
			return nil
		},
	}
	addPlanFlag(cmd, &planID)
	cmd.Flags().BoolVar(&current, "current", false, "authorize this machine's public IP address")
	return cmd
}

func newIPAuthRemoveCmd(flags *rootFlags) *cobra.Command {
	var planID int
	cmd := &cobra.Command{
		Use:   "remove <id-or-ip>",
		Short: "Remove an IP authorization by ID or by IP address",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			params := webshare.IPAuthorizationListParams{}
			getParams := webshare.IPAuthorizationGetParams{}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
				getParams.PlanID = webshare.Int(planID)
			}
			id, err := strconv.Atoi(args[0])
			if err != nil {
				// Not a number: resolve the IP address to its authorization ID.
				if net.ParseIP(args[0]) == nil {
					return usagef("%q is neither an authorization ID nor an IP address", args[0])
				}
				found := false
				for auth, err := range client.IPAuthorizations.ListAll(ctx, params) {
					if err != nil {
						return err
					}
					if auth.IPAddress == args[0] {
						id, found = auth.ID, true
						break
					}
				}
				if !found {
					return fmt.Errorf("no IP authorization found for %s", args[0])
				}
			}
			if err := client.IPAuthorizations.Delete(ctx, id, getParams); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "removed IP authorization %d\n", id)
			return nil
		},
	}
	addPlanFlag(cmd, &planID)
	return cmd
}
