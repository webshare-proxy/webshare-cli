package app

import (
	"context"

	webshare "github.com/webshare-proxy/webshare-go"
)

// ListProxies pages through the proxy list and stops at limit, 0 meaning no
// limit. truncated reports whether more proxies exist past the limit.
func ListProxies(ctx context.Context, client *webshare.Client, params webshare.ProxyListParams, limit int) (proxies []webshare.Proxy, truncated bool, err error) {
	// Read one past the limit so we can tell a list that happens to end
	// exactly at the limit from one that was cut short.
	for proxy, err := range client.Proxies.ListAll(ctx, params) {
		if err != nil {
			return nil, false, err
		}
		if limit > 0 && len(proxies) == limit {
			return proxies, true, nil
		}
		proxies = append(proxies, proxy)
	}
	return proxies, false, nil
}

// ProxyHost returns the address to connect to: the proxy's own address in
// direct mode, or the backbone host when the API returns none (residential
// plans).
func ProxyHost(p webshare.Proxy) string {
	if p.ProxyAddress != nil {
		return *p.ProxyAddress
	}
	return webshare.BackboneHost
}
