// Command syncer syncs Spotify Liked Songs into a public mirror playlist and
// genre playlists, and provides reverse-sync and playlist-merge utilities.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const (
	exitSuccess = 0
	exitError   = 1
	exitUsage   = 2
)

const usageText = `Usage: syncer <command> [flags]

Commands:
  auth      Perform interactive OAuth login and save the token
  run       Sync liked songs to the mirror and genre playlists
  reverse   Add all songs from a playlist to Liked Songs
  merge     Merge two or more playlists into a new playlist

Global flags (accepted by every command):
  --config  Path to config file (default: config.yaml in current directory)

Run 'syncer <command> --help' for command flags.
`

// errUsage marks errors caused by invalid command-line usage. Errors that wrap
// it exit with code 2; configuration errors (errConfig) do too.
var (
	errUsage  = errors.New("usage error")
	errConfig = errors.New("configuration error")
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run executes the command line args and returns the process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	err := dispatch(ctx, args, stdout, stderr)

	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return exitSuccess
	case errors.Is(err, errUsage), errors.Is(err, errConfig):
		fmt.Fprintf(stderr, "syncer: %v\n", err)
		return exitUsage
	default:
		fmt.Fprintf(stderr, "syncer: %v\n", err)
		return exitError
	}
}

func dispatch(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return fmt.Errorf("%w: no command given", errUsage)
	}

	command, rest := args[0], args[1:]

	switch command {
	case "auth":
		return authCommand(ctx, rest, stderr)
	case "run":
		return runCommand(ctx, rest, stdout, stderr)
	case "reverse":
		return reverseCommand(ctx, rest, stdout, stderr)
	case "merge":
		return mergeCommand(ctx, rest, stdout, stderr)
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usageText)
		return nil
	default:
		fmt.Fprint(stderr, usageText)
		return fmt.Errorf("%w: unknown command %q", errUsage, command)
	}
}
