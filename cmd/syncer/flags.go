package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
)

// newFlagSet returns a flag set for a subcommand that reports errors instead
// of exiting, writes help to stderr, and registers the global --config flag.
func newFlagSet(name, synopsis string, stderr io.Writer, configPath *string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: syncer %s\n\nFlags:\n", synopsis)
		fs.PrintDefaults()
	}
	fs.StringVar(configPath, "config", "", "Path to config file (default: config.yaml in current directory)")

	return fs
}

// parseInterspersed parses fs from args, allowing flags to appear before,
// between and after positional arguments, and returns the positional
// arguments in order. Args are split at the first literal "--" token before
// parsing: everything after it is positional verbatim, so "--" can never be
// consumed as a flag value.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var verbatim []string

	if i := slices.Index(args, "--"); i >= 0 {
		args, verbatim = args[:i], args[i+1:]
	}

	var positional []string

	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %w", errUsage, err)
		}

		args = fs.Args()
		if len(args) == 0 {
			return append(positional, verbatim...), nil
		}

		positional = append(positional, args[0])
		args = args[1:]
	}
}

// stringList is a flag.Value collecting strings from repeated flags and
// comma-separated values: --source a --source b,c yields [a b c].
type stringList []string

func (l *stringList) String() string {
	return strings.Join(*l, ",")
}

func (l *stringList) Set(value string) error {
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			*l = append(*l, item)
		}
	}
	return nil
}
