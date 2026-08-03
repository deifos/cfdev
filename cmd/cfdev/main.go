package main

import (
	"context"
	"fmt"
	"os"

	"github.com/deifos/cfdev/internal/app"
	"github.com/deifos/cfdev/internal/config"
	"github.com/deifos/cfdev/internal/inspector"
	"github.com/deifos/cfdev/internal/updater"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "__cfdev_inspector" {
		token := os.Getenv("CFDEV_INSPECTOR_TOKEN")
		paths, err := config.ResolvePaths()
		if err == nil && token == "" {
			err = fmt.Errorf("missing inspector control token")
		}
		if err == nil {
			err = inspector.Run(paths, token, app.Version)
		}
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if handled, code := updater.HandleInternal(os.Args[1:]); handled {
		os.Exit(code)
	}
	updater.CleanupStaged()
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	application := app.New(os.Stdin, os.Stdout, os.Stderr, cwd)
	os.Exit(application.Run(context.Background(), os.Args[1:]))
}
