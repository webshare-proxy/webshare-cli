// Package app holds what the CLI and the TUI do against the Webshare API,
// free of cobra and of any rendering, so both front-ends share one behaviour.
package app

import (
	"context"

	webshare "github.com/webshare-proxy/webshare-go"
)

// Account is the account overview: profile, subscription and active plan.
// Fields are in alphabetical order to keep the --json output stable.
type Account struct {
	// Plan is nil when the subscription has no active plan.
	Plan         *webshare.Plan         `json:"plan"`
	Profile      *webshare.Profile      `json:"profile"`
	Subscription *webshare.Subscription `json:"subscription"`
}

// GetAccount fetches the profile, the subscription and, when there is one,
// the active plan.
func GetAccount(ctx context.Context, client *webshare.Client) (*Account, error) {
	profile, err := client.Profile.Get(ctx)
	if err != nil {
		return nil, err
	}
	subscription, err := client.Subscription.Get(ctx)
	if err != nil {
		return nil, err
	}
	account := &Account{Profile: profile, Subscription: subscription}
	if subscription.Plan != 0 {
		account.Plan, err = client.Plans.Get(ctx, subscription.Plan)
		if err != nil {
			return nil, err
		}
	}
	return account, nil
}
