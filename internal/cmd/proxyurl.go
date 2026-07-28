package cmd

import (
	"fmt"
	"math/rand/v2"
	"strconv"

	"github.com/spf13/cobra"
	webshare "github.com/webshare-proxy/webshare-go"
)

func newProxyURLCmd(flags *rootFlags) *cobra.Command {
	var (
		planID    int
		mode      string
		scheme    string
		username  string
		password  string
		address   string
		port      int
		countries []string
		city      string
		session   string
		rotate    bool
		sessions  int
	)
	cmd := &cobra.Command{
		Use:   "proxy-url",
		Short: "Build ready-to-use proxy connection URLs",
		Long: `Build proxy connection URLs, including the backbone username grammar for
country and city targeting, sticky sessions and rotation.

When no --username/--password is given, your proxy credentials are fetched
from the proxy configuration automatically. When --address is given the mode
defaults to direct; otherwise it defaults to backbone (p.webshare.io).`,
		Example: `  webshare proxy-url --country us --rotate
  webshare proxy-url --country us --sessions 5
  curl --proxy "$(webshare proxy-url --rotate)" https://ipv4.webshare.io/`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if mode == "" {
				if address != "" {
					mode = "direct"
				} else {
					mode = "backbone"
				}
			}
			if session != "" && sessions > 0 {
				return usagef("--session and --sessions are mutually exclusive")
			}
			if rotate && sessions > 0 {
				return usagef("--rotate and --sessions are mutually exclusive")
			}
			if username == "" && password == "" {
				client, err := newClient(flags)
				if err != nil {
					return err
				}
				ctx := cmd.Context()
				resolvedPlan, err := resolvePlanID(ctx, client, planID)
				if err != nil {
					return err
				}
				status, err := client.ProxyConfig.GetStatus(ctx, resolvedPlan)
				if err != nil {
					return fmt.Errorf("fetching proxy credentials from the proxy config: %w", err)
				}
				username, password = status.Username, status.Password
			}
			build := func(sessionID string) (string, error) {
				return webshare.ProxyURL(webshare.ProxyURLParams{
					Mode:         webshare.ConnectionMode(mode),
					Scheme:       scheme,
					Username:     username,
					Password:     password,
					Address:      address,
					Port:         port,
					CountryCodes: countries,
					City:         city,
					SessionID:    sessionID,
					Rotate:       rotate,
				})
			}
			if sessions > 0 {
				for range sessions {
					url, err := build(strconv.Itoa(rand.IntN(1_000_000_000)))
					if err != nil {
						return err
					}
					fmt.Println(url)
				}
				return nil
			}
			url, err := build(session)
			if err != nil {
				return err
			}
			fmt.Println(url)
			return nil
		},
	}
	addPlanFlag(cmd, &planID)
	cmd.Flags().StringVar(&mode, "mode", "", "connection mode: direct or backbone (default: backbone, or direct when --address is set)")
	cmd.Flags().StringVar(&scheme, "scheme", "", "URL scheme (default http)")
	cmd.Flags().StringVar(&username, "username", "", "proxy username (default: fetched from the proxy config)")
	cmd.Flags().StringVar(&password, "password", "", "proxy password (default: fetched from the proxy config)")
	cmd.Flags().StringVar(&address, "address", "", "proxy address (direct mode)")
	cmd.Flags().IntVar(&port, "port", 0, "proxy port (default 80 in backbone mode)")
	cmd.Flags().StringSliceVar(&countries, "country", nil, "target country codes (backbone mode)")
	cmd.Flags().StringVar(&city, "city", "", "target city (backbone mode, residential plans)")
	cmd.Flags().StringVar(&session, "session", "", "numeric sticky-session ID (backbone mode)")
	cmd.Flags().BoolVar(&rotate, "rotate", false, "use a new IP for every request (backbone mode)")
	cmd.Flags().IntVar(&sessions, "sessions", 0, "generate N sticky-session URLs with random session IDs")
	return cmd
}
