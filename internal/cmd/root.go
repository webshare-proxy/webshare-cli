// Package cmd implements the webshare CLI commands.
package cmd

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

// version is the CLI version, overridden at release time via
// -ldflags "-X github.com/webshare-proxy/webshare-cli/internal/cmd.version=vX.Y.Z".
var version = "dev"

// rootFlags holds the global flags shared by every command.
type rootFlags struct {
	baseURL  string
	asJSON   bool
	insecure bool
}

func newRootCmd() (*cobra.Command, *rootFlags) {
	flags := &rootFlags{}
	root := &cobra.Command{
		Use:   "webshare",
		Short: "Manage Webshare proxies from the command line",
		Long: `webshare manages your Webshare proxies, plans and account from the command line.

Authentication reads the WEBSHARE_API_KEY environment variable; create a key
on the API Keys page of the Webshare dashboard.

Output adapts to where it goes: tables on a terminal, tab-separated values in
a pipe, and --format csv/json/txt where structured output is useful.`,
		Example: `  # Feed your proxy list to another tool
  webshare proxies list --limit 0 > proxies.txt
  webshare proxies list --format csv --limit 0 > proxies.csv

  # Authorize this machine's IP for credential-less proxy use
  webshare ipauth add --current

  # Build ready-to-use rotating proxy URLs
  webshare proxy-url --country us --rotate

  # Check usage over the last day
  webshare stats --since 24h`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.Version = version
	root.SetVersionTemplate("webshare version {{.Version}}\n")
	root.PersistentFlags().StringVar(&flags.baseURL, "base-url", "", "API base URL (default https://proxy.webshare.io, env WEBSHARE_BASE_URL)")
	root.PersistentFlags().BoolVar(&flags.asJSON, "json", false, "output JSON instead of the default format")
	root.PersistentFlags().BoolVar(&flags.insecure, "insecure", false, "skip TLS certificate verification (for test environments only)")
	_ = root.PersistentFlags().MarkHidden("insecure")

	root.AddCommand(
		newProxiesCmd(flags),
		newProxyURLCmd(flags),
		newPlansCmd(flags),
		newStatsCmd(flags),
		newActivityCmd(flags),
		newIPAuthCmd(flags),
		newIPCmd(flags),
		newWhoamiCmd(flags),
		newAccountCmd(flags),
		newSubusersCmd(flags),
		newConfigCmd(flags),
		newTransactionsCmd(flags),
		newInvoicesCmd(flags),
		newNotificationsCmd(flags),
	)
	return root, flags
}

// Execute runs the CLI and returns the process exit code: 0 on success, 1 on
// runtime/API errors, 2 on usage errors.
func Execute(ctx context.Context) int {
	return run(ctx, os.Args[1:])
}

func run(ctx context.Context, args []string) int {
	root, _ := newRootCmd()
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	styler := output.NewStyler(os.Stderr)
	fmt.Fprintln(os.Stderr, styler.Red("error:")+" "+renderError(err))

	var usageErr *usageError
	if errors.As(err, &usageErr) {
		return 2
	}
	return 1
}

// usageError marks errors caused by invalid invocation rather than a failed
// operation; they exit with code 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// renderError turns SDK and CLI errors into a friendly one-or-few-line
// message.
func renderError(err error) string {
	var apiErr *webshare.Error
	if errors.As(err, &apiErr) {
		var lines []string
		if apiErr.Detail != "" {
			lines = append(lines, apiErr.Detail)
		}
		for field, msgs := range apiErr.FieldErrors {
			lines = append(lines, fmt.Sprintf("%s: %s", field, strings.Join(msgs, "; ")))
		}
		if len(lines) == 0 {
			lines = append(lines, fmt.Sprintf("the API returned HTTP %d", apiErr.StatusCode))
		}
		if apiErr.StatusCode == 401 {
			lines = append(lines, "check that WEBSHARE_API_KEY holds a valid API key")
		}
		if apiErr.Code != "" {
			ref := "(code " + apiErr.Code
			if apiErr.RequestID != "" {
				ref += ", request id " + apiErr.RequestID
			}
			lines = append(lines, ref+")")
		}
		return strings.Join(lines, "\n  ")
	}
	var reqErr *webshare.RequestError
	if errors.As(err, &reqErr) {
		if errors.Is(err, context.Canceled) {
			return "interrupted"
		}
		return "could not reach the Webshare API: " + reqErr.Unwrap().Error()
	}
	return err.Error()
}

// newClient builds the SDK client from the environment and global flags.
func newClient(flags *rootFlags) (*webshare.Client, error) {
	opts := []webshare.RequestOption{
		webshare.WithSource("WebshareCLI/" + version + " (Go; " + runtime.Version() + ")"),
	}
	if flags.insecure {
		opts = append(opts, webshare.WithHTTPClient(&http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		}))
	}
	baseURL := flags.baseURL
	if baseURL == "" {
		baseURL = os.Getenv("WEBSHARE_BASE_URL")
	}
	if baseURL != "" {
		opts = append(opts, webshare.WithBaseURL(baseURL))
	}
	client, err := webshare.NewClient(opts...)
	if err != nil {
		if os.Getenv("WEBSHARE_API_KEY") == "" {
			return nil, errors.New("WEBSHARE_API_KEY is not set — create an API key on the API Keys page of the Webshare dashboard and export it:\n  export WEBSHARE_API_KEY=your-key")
		}
		return nil, err
	}
	return client, nil
}

// resolvePlanID returns the plan to operate on: the --plan flag when given,
// otherwise the account's active plan.
func resolvePlanID(ctx context.Context, client *webshare.Client, flagValue int) (int, error) {
	if flagValue > 0 {
		return flagValue, nil
	}
	subscription, err := client.Subscription.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("resolving the active plan: %w", err)
	}
	if subscription.Plan == 0 {
		return 0, errors.New("the account has no active plan; pass --plan explicitly")
	}
	return subscription.Plan, nil
}

// addPlanFlag registers the --plan flag shared by plan-scoped commands.
func addPlanFlag(cmd *cobra.Command, target *int) {
	cmd.Flags().IntVar(target, "plan", 0, "plan ID (default: the account's active plan)")
}

// confirm asks for interactive confirmation on a terminal. Non-interactive
// runs must pass --yes.
func confirm(cmd *cobra.Command, yes bool, prompt string) error {
	if yes {
		return nil
	}
	if !output.IsTerminal(os.Stdin) || !output.IsTerminal(os.Stderr) {
		return usagef("refusing to %s without confirmation; pass --yes in non-interactive use", prompt)
	}
	fmt.Fprintf(os.Stderr, "%s? [y/N] ", strings.ToUpper(prompt[:1])+prompt[1:])
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading confirmation: %w", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		return errors.New("aborted")
	}
	return nil
}
