// Command webshare is the official command-line interface for the Webshare
// proxy API (https://apidocs.webshare.io).
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/webshare-proxy/webshare-cli/internal/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(cmd.Execute(ctx))
}
