package main

import (
	"context"
	"os"

	"github.com/deifos/cfdev/internal/app"
	"github.com/deifos/cfdev/internal/updater"
)

func main() {
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
