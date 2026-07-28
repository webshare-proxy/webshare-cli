package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

func newSubusersCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subusers",
		Short: "Manage sub-users of your plan",
	}
	cmd.AddCommand(
		newSubusersListCmd(flags),
		newSubusersCreateCmd(flags),
		newSubusersUpdateCmd(flags),
		newSubusersDeleteCmd(flags),
	)
	return cmd
}

func newSubusersListCmd(flags *rootFlags) *cobra.Command {
	var planID int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sub-users",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			params := webshare.SubuserListParams{}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
			}
			var subusers []webshare.Subuser
			for subuser, err := range client.Subusers.ListAll(cmd.Context(), params) {
				if err != nil {
					return err
				}
				subusers = append(subusers, subuser)
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, subusers)
			}
			rows := make([][]string, 0, len(subusers))
			for _, s := range subusers {
				limit := "unlimited"
				if s.ProxyLimit > 0 {
					limit = fmt.Sprintf("%g GB", s.ProxyLimit)
				}
				rows = append(rows, []string{
					strconv.Itoa(s.ID), s.Label, limit,
					strconv.Itoa(s.MaxThreadCount),
					formatBytes(s.AggregateStats.BandwidthTotal),
				})
			}
			return output.Table(os.Stdout, []string{"ID", "LABEL", "BANDWIDTH LIMIT", "MAX THREADS", "USED"}, rows)
		},
	}
	addPlanFlag(cmd, &planID)
	return cmd
}

func newSubusersCreateCmd(flags *rootFlags) *cobra.Command {
	var (
		planID     int
		label      string
		proxyLimit float64
		maxThreads int
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a sub-user",
		Example: `  webshare subusers create --label customer-1
  webshare subusers create --label customer-2 --bandwidth 50 --max-threads 100`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if label == "" {
				return usagef("--label is required")
			}
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			params := webshare.SubuserCreateParams{Label: label}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
			}
			if cmd.Flags().Changed("bandwidth") {
				params.ProxyLimit = webshare.Float64(proxyLimit)
			}
			if cmd.Flags().Changed("max-threads") {
				params.MaxThreadCount = webshare.Int(maxThreads)
			}
			subuser, err := client.Subusers.Create(cmd.Context(), params)
			if err != nil {
				return err
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, subuser)
			}
			fmt.Printf("created sub-user %d (%s)\n", subuser.ID, subuser.Label)
			return nil
		},
	}
	addPlanFlag(cmd, &planID)
	cmd.Flags().StringVar(&label, "label", "", "label identifying the sub-user (required)")
	cmd.Flags().Float64Var(&proxyLimit, "bandwidth", 0, "bandwidth limit in GB (0 for unlimited)")
	cmd.Flags().IntVar(&maxThreads, "max-threads", 0, "maximum concurrent proxy requests")
	return cmd
}

func newSubusersUpdateCmd(flags *rootFlags) *cobra.Command {
	var (
		planID     int
		label      string
		proxyLimit float64
		maxThreads int
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a sub-user (only the given flags change)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.Atoi(args[0])
			if err != nil {
				return usagef("sub-user ID must be a number, got %q", args[0])
			}
			params := webshare.SubuserUpdateParams{}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
			}
			changed := false
			if cmd.Flags().Changed("label") {
				params.Label = webshare.String(label)
				changed = true
			}
			if cmd.Flags().Changed("bandwidth") {
				params.ProxyLimit = webshare.Float64(proxyLimit)
				changed = true
			}
			if cmd.Flags().Changed("max-threads") {
				params.MaxThreadCount = webshare.Int(maxThreads)
				changed = true
			}
			if !changed {
				return usagef("nothing to update; pass --label, --bandwidth or --max-threads")
			}
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			subuser, err := client.Subusers.Update(cmd.Context(), id, params)
			if err != nil {
				return err
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, subuser)
			}
			fmt.Printf("updated sub-user %d (%s)\n", subuser.ID, subuser.Label)
			return nil
		},
	}
	addPlanFlag(cmd, &planID)
	cmd.Flags().StringVar(&label, "label", "", "new label")
	cmd.Flags().Float64Var(&proxyLimit, "bandwidth", 0, "bandwidth limit in GB (0 for unlimited)")
	cmd.Flags().IntVar(&maxThreads, "max-threads", 0, "maximum concurrent proxy requests")
	return cmd
}

func newSubusersDeleteCmd(flags *rootFlags) *cobra.Command {
	var (
		planID int
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a sub-user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.Atoi(args[0])
			if err != nil {
				return usagef("sub-user ID must be a number, got %q", args[0])
			}
			if err := confirm(cmd, yes, fmt.Sprintf("delete sub-user %d", id)); err != nil {
				return err
			}
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			params := webshare.SubuserGetParams{}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
			}
			if err := client.Subusers.Delete(cmd.Context(), id, params); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "deleted sub-user %d\n", id)
			return nil
		},
	}
	addPlanFlag(cmd, &planID)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}
