package main

import (
	"context"
	"os"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/app"
)

func main() {
	application, err := app.New(app.Dependencies{})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(application.Run(context.Background(), os.Args[1:]))
}
