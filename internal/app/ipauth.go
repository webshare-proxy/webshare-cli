package app

import (
	"context"
	"fmt"

	webshare "github.com/webshare-proxy/webshare-go"
)

// FindIPAuthorizationID returns the ID of the authorization for ip.
func FindIPAuthorizationID(ctx context.Context, client *webshare.Client, params webshare.IPAuthorizationListParams, ip string) (int, error) {
	for auth, err := range client.IPAuthorizations.ListAll(ctx, params) {
		if err != nil {
			return 0, err
		}
		if auth.IPAddress == ip {
			return auth.ID, nil
		}
	}
	return 0, fmt.Errorf("no IP authorization found for %s", ip)
}
