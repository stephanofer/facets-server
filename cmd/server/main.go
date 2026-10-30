// Command server runs HTTP, durable workers and explicit operational tasks.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/stephanofer/facets-server/internal/app"
	"github.com/stephanofer/facets-server/internal/config"
	"github.com/stephanofer/facets-server/internal/telemetry"
)

func main() {
	mode := flag.String("mode", "serve", "serve, worker, migrate, maintenance, staff-create, job-status, job-retry, openapi, version")
	id := flag.String("job", "", "job reference")
	operator := flag.String("operator", "", "real operational identity for audited retry")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch *mode {
	case "version":
		err = app.Version(os.Stdout)
	case "openapi":
		err = app.OpenAPI(os.Stdout)
	default:
		var c config.Config
		c, err = config.Load(os.Getenv)
		if err != nil {
			// Configuration errors contain only our fixed messages/key names, not values.
			log.Error("configuration rejected", "reason", err.Error())
			os.Exit(1)
		}
		switch *mode {
		case "staff-create":
			err = app.ProvisionStaff(ctx, c, *operator, os.Stdin, os.Stdout)
		case "migrate":
			err = app.Migrate(ctx, c.MigrationURL)
		default:
			err = app.Run(ctx, c, *mode, *id, *operator, log, os.Stdout)
		}
	}
	if err != nil {
		telemetry.LogError(ctx, log, "command failed", err)
		os.Exit(1)
	}
}
