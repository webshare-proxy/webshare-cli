package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/webshare-proxy/webshare-cli/internal/auth"
	"github.com/webshare-proxy/webshare-cli/internal/output"
)

// oauthClientID identifies this CLI to the authorization server. A public
// client has no secret to keep, so the id ships in the binary; point it
// somewhere else with WEBSHARE_OAUTH_CLIENT_ID to test against another
// environment.
const oauthClientID = "webshare-cli"

func authConfig(flags *rootFlags, scopes []string) auth.Config {
	clientID := os.Getenv("WEBSHARE_OAUTH_CLIENT_ID")
	if clientID == "" {
		clientID = oauthClientID
	}
	return auth.Config{
		BaseURL:    resolveBaseURL(flags),
		ClientID:   clientID,
		Scopes:     scopes,
		HTTPClient: httpClient(flags),
		Output:     os.Stderr,
	}
}

func newLoginCmd(flags *rootFlags) *cobra.Command {
	var scopes []string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to Webshare through your browser",
		Long: `login signs this machine in to Webshare.

It opens your browser, waits for you to approve the access it asks for, and
stores the token in your operating system's keychain. When there is no
keychain it falls back to a file only you can read.

WEBSHARE_API_KEY keeps working and takes precedence over a stored login.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			credentials, err := auth.Login(cmd.Context(), authConfig(flags, scopes))
			if err != nil {
				return err
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, map[string]any{
					"scopes": credentials.Scopes, "expires_at": credentials.Expiry,
				})
			}
			fmt.Fprintln(os.Stderr, "Signed in. Granted: "+strings.Join(credentials.Scopes, ", "))
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&scopes, "scope", nil,
		"access to ask for (default: everything the CLI's commands need)")
	return cmd
}

func newLogoutCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Revoke this machine's stored login",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			config := authConfig(flags, nil)
			credentials, err := auth.Load(config.BaseURL)
			if errors.Is(err, auth.ErrNoCredentials) {
				fmt.Fprintln(os.Stderr, "Not signed in.")
				return nil
			}
			if err != nil {
				return err
			}
			if err := auth.Logout(cmd.Context(), config, credentials); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "Signed out.")
			return nil
		},
	}
}

// credentialSource names which of the two ways in the command used, so a
// surprising answer from whoami points at the credential rather than the API.
func credentialSource() string {
	if os.Getenv("WEBSHARE_API_KEY") != "" {
		return "WEBSHARE_API_KEY"
	}
	return "the login stored by `webshare login`"
}
