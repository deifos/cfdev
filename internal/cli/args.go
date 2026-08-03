package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/deifos/cfdev/internal/failure"
)

type Options struct {
	JSON          bool
	Quiet         bool
	Verbose       bool
	Force         bool
	All           bool
	Claim         bool
	Yes           bool
	Detach        bool
	CaptureBodies bool
	Help          bool
	Version       bool
}

type Invocation struct {
	Command string
	Args    []string
	Options Options
}

func Parse(args []string) (Invocation, error) {
	var options Options
	positional := make([]string, 0, len(args))
	literal := false

	for _, arg := range args {
		if literal {
			positional = append(positional, arg)
			continue
		}
		if arg == "--" {
			literal = true
			continue
		}
		switch arg {
		case "--json":
			options.JSON = true
		case "--quiet", "-q":
			options.Quiet = true
		case "--verbose", "-v":
			options.Verbose = true
		case "--force", "-f":
			options.Force = true
		case "--all":
			options.All = true
		case "--yes", "-y":
			options.Yes = true
		case "--detach", "--background", "-d":
			options.Detach = true
		case "--capture-bodies":
			options.CaptureBodies = true
		case "--help", "-h":
			options.Help = true
		case "--version", "-V":
			options.Version = true
		default:
			if strings.HasPrefix(arg, "-") {
				return Invocation{}, usage(fmt.Sprintf("unknown option: %s", arg), "Run `cfdev --help` to see available options.")
			}
			positional = append(positional, arg)
		}
	}

	if len(positional) == 0 {
		if options.Version {
			return Invocation{Command: "version", Options: options}, nil
		}
		if options.Help {
			return Invocation{Command: "help", Options: options}, nil
		}
		return Invocation{Command: "dashboard", Options: options}, nil
	}

	command := strings.ToLower(positional[0])
	if _, err := strconv.Atoi(command); err == nil {
		return Invocation{Command: "shortcut", Args: positional, Options: options}, nil
	}
	switch command {
	case "ls":
		command = "list"
	case "rm":
		command = "remove"
	}
	return Invocation{Command: command, Args: positional[1:], Options: options}, nil
}

func usage(message, hint string) error {
	err := failure.New("INVALID_USAGE", message, failure.ExitUsage)
	err.Hint = hint
	return err
}

func RequireArgs(inv Invocation, count int, example string) error {
	if len(inv.Args) == count {
		return nil
	}
	return usage(fmt.Sprintf("`cfdev %s` expects %d argument(s)", inv.Command, count), "Try `"+example+"`.")
}
