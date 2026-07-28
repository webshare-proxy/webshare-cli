package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

func newNotificationsCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notifications",
		Short: "View and dismiss account notifications",
	}

	var all bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List account notifications (undismissed by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			params := webshare.NotificationListParams{}
			if !all {
				params.DismissedAtIsNull = webshare.Bool(true)
			}
			var notifications []webshare.Notification
			for notification, err := range client.Notifications.ListAll(cmd.Context(), params) {
				if err != nil {
					return err
				}
				notifications = append(notifications, notification)
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, notifications)
			}
			rows := make([][]string, 0, len(notifications))
			for _, n := range notifications {
				state := "new"
				if n.DismissedAt != nil {
					state = "dismissed"
				}
				rows = append(rows, []string{
					strconv.Itoa(n.ID), n.Type, state,
					n.CreatedAt.Local().Format("2006-01-02 15:04"),
				})
			}
			return output.Table(os.Stdout, []string{"ID", "TYPE", "STATE", "WHEN"}, rows)
		},
	}
	list.Flags().BoolVar(&all, "all", false, "include dismissed notifications")

	dismiss := &cobra.Command{
		Use:   "dismiss <id>",
		Short: "Dismiss a notification",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.Atoi(args[0])
			if err != nil {
				return usagef("notification ID must be a number, got %q", args[0])
			}
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			if _, err := client.Notifications.Dismiss(cmd.Context(), id); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "dismissed notification %d\n", id)
			return nil
		},
	}

	cmd.AddCommand(list, dismiss)
	return cmd
}
