# webshare-cli

`webshare` is the official command-line interface for the
[Webshare proxy API](https://apidocs.webshare.io), built on
[webshare-go](https://github.com/webshare-proxy/webshare-go).

## Install

Download a prebuilt binary for your platform from the
[releases page](https://github.com/webshare-proxy/webshare-cli/releases) —
no Go required. Binaries are published for Linux, macOS and Windows on
amd64 and arm64; each release includes a `checksums.txt`.

```sh
# example: macOS on Apple Silicon
tar -xzf webshare_*_darwin_arm64.tar.gz
sudo mv webshare /usr/local/bin/
```

With Go 1.23+ installed you can build from source instead:

```sh
go install github.com/webshare-proxy/webshare-cli@latest
```

## Authentication

The CLI reads the `WEBSHARE_API_KEY` environment variable. Create a key on
the API Keys page of the Webshare dashboard and export it:

```sh
export WEBSHARE_API_KEY=your-key
webshare whoami
```

## It just works with other tools

Output adapts to where it goes. On a terminal you get aligned tables; in a
pipe you get machine-readable output with no headers or colors:

```sh
# Plain address:port:username:password lines — the format most
# proxy-consuming tools accept directly
webshare proxies list > proxies.txt

# CSV for tools that want structured proxy lists
webshare proxies list --format csv > proxies.csv

# JSON everywhere, for jq and friends
webshare proxies list --json | jq '.[].country_code'
webshare stats --json | jq .bandwidth_total

# Compose with curl: a rotating backbone proxy URL in one line
curl --proxy "$(webshare proxy-url --rotate)" https://ipv4.webshare.io/
```

Colors honor [`NO_COLOR`](https://no-color.org) and `TERM=dumb`. Errors go
to stderr; exit codes are `0` (success), `1` (API/network error), `2`
(usage error).

## Commands

| Command | Purpose |
|---|---|
| `webshare proxies list` | List proxies as a table, txt, csv or json (`--country`, `--limit`) |
| `webshare proxies download` | Server-rendered proxy list; download token fetched automatically |
| `webshare proxies refresh` | Replace the whole proxy list (asks for confirmation) |
| `webshare proxies replaced` | Show replaced proxies and their successors |
| `webshare proxy-url` | Build proxy URLs; `--country`, `--session`, `--rotate`, `--sessions N` |
| `webshare stats` | Usage aggregate or `--hourly` series (`--since 24h`, `7d`, …) |
| `webshare activity list` | Recent proxy requests (`--error '*'` for failures only) |
| `webshare activity export` | Server-rendered CSV export of proxy activity |
| `webshare ipauth list/add/remove` | IP authorizations; `add --current` detects your IP |
| `webshare ip` | Your current public IP |
| `webshare whoami` / `account` | Account, subscription and active plan |
| `webshare plans list/show` | Your proxy plans |
| `webshare config show/set` | Proxy configuration (credentials, timeouts) |
| `webshare subusers …` | Manage sub-users |
| `webshare transactions list` | Payment history |
| `webshare invoices download` | Invoice PDF for a transaction |
| `webshare notifications …` | Account notifications |

Every plan-scoped command accepts `--plan`; without it the account's active
plan is used. `webshare completion bash|zsh|fish` generates shell
completions.

## Everyday examples

```sh
# Authorize this machine's IP so tools can use proxies without credentials
webshare ipauth add --current

# Five sticky-session URLs for a worker pool
webshare proxy-url --country us --sessions 5

# What failed in the last 15 minutes?
webshare activity list --since 15m --error '*'

# Feed only French proxies to a scraper
webshare proxies list --country fr --format csv > fr.csv

# Download last month's invoice
webshare transactions list
webshare invoices download 41
```

## Development

```sh
go build -o webshare .
go test ./...
```

`--base-url` (or `WEBSHARE_BASE_URL`) points the CLI at another API
environment.

## License

MIT, see [LICENSE](LICENSE).
