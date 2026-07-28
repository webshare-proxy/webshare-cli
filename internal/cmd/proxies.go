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

func newProxiesCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proxies",
		Short: "List, download and refresh your proxy list",
	}
	cmd.AddCommand(
		newProxiesListCmd(flags),
		newProxiesDownloadCmd(flags),
		newProxiesRefreshCmd(flags),
		newProxiesReplacedCmd(flags),
	)
	return cmd
}

// proxyHost returns the address to connect to: the proxy's own address in
// direct mode, or the backbone host when the API returns none (residential
// plans).
func proxyHost(p webshare.Proxy) string {
	if p.ProxyAddress != nil {
		return *p.ProxyAddress
	}
	return webshare.BackboneHost
}

func newProxiesListCmd(flags *rootFlags) *cobra.Command {
	var (
		planID    int
		mode      string
		countries []string
		limit     int
		format    string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the proxies in your plan",
		Long: `List the proxies in your plan.

On a terminal the result is a table. In a pipe it is plain
address:port:username:password lines — the format most proxy-consuming tools
accept directly. Use --format to force txt, csv, json or table.`,
		Example: `  webshare proxies list
  webshare proxies list --country us,fr > proxies.txt
  webshare proxies list --format csv > proxies.csv
  curl --proxy "$(webshare proxies list --limit 1 | awk -F: '{print "http://"$3":"$4"@"$1":"$2}')" https://ipv4.webshare.io/`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			params := webshare.ProxyListParams{Mode: webshare.ConnectionMode(mode)}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
			}
			for _, c := range countries {
				params.CountryCodeIn = append(params.CountryCodeIn, strings.ToUpper(c))
			}
			var proxies []webshare.Proxy
			for proxy, err := range client.Proxies.ListAll(cmd.Context(), params) {
				if err != nil {
					return err
				}
				proxies = append(proxies, proxy)
				if limit > 0 && len(proxies) >= limit {
					break
				}
			}
			return writeProxies(cmd, flags, proxies, format)
		},
	}
	addPlanFlag(cmd, &planID)
	cmd.Flags().StringVar(&mode, "mode", "direct", "connection mode: direct or backbone (residential plans need backbone)")
	cmd.Flags().StringSliceVar(&countries, "country", nil, "filter by country codes (e.g. us,fr)")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of proxies (default: all)")
	cmd.Flags().StringVar(&format, "format", "auto", "output format: auto, table, txt, csv or json")
	return cmd
}

func writeProxies(cmd *cobra.Command, flags *rootFlags, proxies []webshare.Proxy, format string) error {
	if flags.asJSON {
		format = "json"
	}
	if format == "auto" || format == "" {
		if output.IsTerminal(os.Stdout) {
			format = "table"
		} else {
			format = "txt"
		}
	}
	switch format {
	case "json":
		return output.JSON(os.Stdout, proxies)
	case "txt":
		for _, p := range proxies {
			fmt.Printf("%s:%d:%s:%s\n", proxyHost(p), p.Port, p.Username, p.Password)
		}
		return nil
	case "csv":
		rows := make([][]string, 0, len(proxies))
		for _, p := range proxies {
			rows = append(rows, []string{
				proxyHost(p), strconv.Itoa(p.Port), p.Username, p.Password,
				p.CountryCode, p.CityName, strconv.FormatBool(p.Valid), p.ID,
			})
		}
		return output.CSV(os.Stdout, []string{"address", "port", "username", "password", "country_code", "city_name", "valid", "id"}, rows)
	case "table":
		styler := output.NewStyler(os.Stdout)
		rows := make([][]string, 0, len(proxies))
		for _, p := range proxies {
			valid := styler.Green("yes")
			if !p.Valid {
				valid = styler.Red("no")
			}
			rows = append(rows, []string{
				p.ID, proxyHost(p), strconv.Itoa(p.Port), p.CountryCode, p.CityName, valid,
			})
		}
		if err := output.Table(os.Stdout, []string{"ID", "ADDRESS", "PORT", "COUNTRY", "CITY", "VALID"}, rows); err != nil {
			return err
		}
		fmt.Println(styler.Dim(fmt.Sprintf("%d proxies", len(proxies))))
		return nil
	default:
		return usagef("unknown format %q (expected auto, table, txt, csv or json)", format)
	}
}

func newProxiesDownloadCmd(flags *rootFlags) *cobra.Command {
	var (
		planID    int
		mode      string
		auth      string
		countries []string
		search    string
		outPath   string
	)
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download the server-rendered proxy list as text",
		Long: `Download the proxy list rendered by the server (one
address:port:username:password line per proxy). The download token is fetched
from your proxy configuration automatically.`,
		Args: cobra.NoArgs,
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
				return fmt.Errorf("fetching the proxy config for the download token: %w", err)
			}
			for i, c := range countries {
				countries[i] = strings.ToUpper(c)
			}
			text, err := client.Proxies.Download(ctx, webshare.ProxyDownloadParams{
				Token:                config.ProxyListDownloadToken,
				CountryCodes:         countries,
				AuthenticationMethod: webshare.AuthenticationMethod(auth),
				EndpointMode:         webshare.ConnectionMode(mode),
				Search:               search,
				PlanID:               webshare.Int(resolvedPlan),
			})
			if err != nil {
				return err
			}
			if outPath != "" && outPath != "-" {
				return os.WriteFile(outPath, []byte(text), 0o644)
			}
			fmt.Print(text)
			return nil
		},
	}
	addPlanFlag(cmd, &planID)
	cmd.Flags().StringVar(&mode, "mode", "direct", "endpoint mode: direct or backbone")
	cmd.Flags().StringVar(&auth, "auth", "username", "authentication method: username or sourceip")
	cmd.Flags().StringSliceVar(&countries, "country", nil, "limit to country codes (e.g. us,fr)")
	cmd.Flags().StringVar(&search, "search", "", "search terms")
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "write to a file instead of stdout")
	return cmd
}

func newProxiesRefreshCmd(flags *rootFlags) *cobra.Command {
	var (
		planID int
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Replace the entire proxy list on demand",
		Long: `Replace the entire proxy list on demand. This uses one of the plan's
on-demand refreshes and changes every proxy in the list.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirm(cmd, yes, "replace the entire proxy list"); err != nil {
				return err
			}
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			params := webshare.ProxyRefreshParams{}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
			}
			if err := client.Proxies.Refresh(cmd.Context(), params); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "proxy list refresh started")
			return nil
		},
	}
	addPlanFlag(cmd, &planID)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newProxiesReplacedCmd(flags *rootFlags) *cobra.Command {
	var (
		planID int
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "replaced",
		Short: "List proxies that were replaced and their successors",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			params := webshare.ReplacedProxyListParams{}
			if planID > 0 {
				params.PlanID = webshare.Int(planID)
			}
			var replaced []webshare.ReplacedProxy
			for item, err := range client.ReplacedProxies.ListAll(cmd.Context(), params) {
				if err != nil {
					return err
				}
				replaced = append(replaced, item)
				if limit > 0 && len(replaced) >= limit {
					break
				}
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, replaced)
			}
			rows := make([][]string, 0, len(replaced))
			for _, r := range replaced {
				rows = append(rows, []string{
					strconv.Itoa(r.ID),
					fmt.Sprintf("%s:%d", r.Proxy, r.ProxyPort),
					fmt.Sprintf("%s:%d", r.ReplacedWith, r.ReplacedWithPort),
					string(r.Reason),
					r.CreatedAt.Format("2006-01-02 15:04"),
				})
			}
			return output.Table(os.Stdout, []string{"ID", "OLD", "NEW", "REASON", "WHEN"}, rows)
		},
	}
	addPlanFlag(cmd, &planID)
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum number of entries (0 for all)")
	return cmd
}
