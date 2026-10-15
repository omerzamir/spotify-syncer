package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"spotify-syncer/internal/auth"
	"spotify-syncer/internal/spotifyio"
	"spotify-syncer/internal/sync"
)

const (
	// defaultLibraryBatchSize is Spotify's maximum number of tracks per
	// save-to-library request.
	defaultLibraryBatchSize = spotifyio.MaxLibraryBatchSize

	// reverseNeedsPlaylistMsg is the usage error for a reverse without a source.
	reverseNeedsPlaylistMsg = "reverse needs a playlist: syncer reverse <playlist> or --playlist <playlist> " +
		"(to reverse the mirror playlist, name it explicitly)"

	// runReverseNeedsPlaylistMsg is the usage error for run --reverse without a source.
	runReverseNeedsPlaylistMsg = "run --reverse needs a playlist: --reverse --playlist <playlist> " +
		"(to reverse the mirror playlist, name it explicitly)"
)

// authCommand performs the interactive OAuth login and saves the token.
func authCommand(ctx context.Context, args []string, stderr io.Writer) error {
	var configPath string

	fs := newFlagSet("auth", "auth [flags]", stderr, &configPath)
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}

	env, err := loadEnvironment(configPath, logText, stderr)
	if err != nil {
		return err
	}

	token, err := auth.InteractiveLogin(ctx, env.logger, env.cfg.Spotify)
	if err != nil {
		return fmt.Errorf("interactive login failed: %w", err)
	}

	if err = env.store.SaveToken(token); err != nil {
		return fmt.Errorf("saving token: %w", err)
	}

	env.logger.InfoContext(ctx, "authentication successful, token saved")

	return nil
}

// runCommand runs the liked-songs sync, or a reverse sync with --reverse.
func runCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var (
		configPath   string
		mode         string
		enableGenres bool
		forceRefresh bool
		reverse      bool
		playlist     string
		dryRun       bool
	)

	fs := newFlagSet("run", "run [flags]", stderr, &configPath)
	fs.StringVar(&mode, "mode", string(sync.ModeFull),
		"Sync mode: full (reconcile everything) or fast (only add tracks liked since the last run)")
	fs.BoolVar(&enableGenres, "genres", true, "Enable genre playlist sync")
	fs.BoolVar(&forceRefresh, "force-refresh", false, "Force refresh of cached playlist contents")
	fs.BoolVar(&reverse, "reverse", false, "Reverse mode: add songs from playlist to liked songs")
	fs.StringVar(&playlist, "playlist", "",
		"Playlist name, ID, or URL for reverse mode (default: mirror_playlist_name from config)")
	fs.BoolVar(&dryRun, "dry-run", false, "Dry run mode (for reverse sync)")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}

	if len(positional) > 0 {
		return fmt.Errorf("%w: run takes no positional arguments, got %q", errUsage, positional)
	}

	runMode, err := sync.ParseMode(mode)
	if err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}

	if reverse {
		if playlist == "" {
			return fmt.Errorf("%w: %s", errUsage, runReverseNeedsPlaylistMsg)
		}

		return runReverse(ctx, configPath, sync.ReverseConfig{
			PlaylistIdentifier: playlist,
			BatchSize:          defaultLibraryBatchSize,
			DryRun:             dryRun,
		}, stdout, stderr)
	}

	s, err := openSession(ctx, configPath, stderr)
	if err != nil {
		return err
	}
	defer s.close(ctx)

	result, err := sync.Run(ctx, s.logger, s.cfg, s.client, s.store, sync.RunOptions{
		Mode:         runMode,
		EnableGenres: enableGenres,
		ForceRefresh: forceRefresh,
	})
	if err != nil {
		return fmt.Errorf("sync run failed: %w", err)
	}

	if err = writeSummary(stdout, newSyncSummary(result, enableGenres)); err != nil {
		return err
	}

	if !result.Success {
		return errors.New("sync run completed with errors")
	}

	return nil
}

// reverseCommand adds every song from a playlist to Liked Songs. The playlist
// is given either as --playlist or as exactly one positional argument.
func reverseCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var (
		configPath string
		reverseCfg sync.ReverseConfig
	)

	fs := newFlagSet("reverse", "reverse <playlist> [flags]", stderr, &configPath)
	fs.StringVar(&reverseCfg.PlaylistIdentifier, "playlist", "",
		"Playlist name, ID, or URL (alternative to the positional argument)")
	fs.BoolVar(&reverseCfg.DryRun, "dry-run", false,
		"Preview tracks that would be added without actually adding them")
	fs.IntVar(&reverseCfg.BatchSize, "batch-size", defaultLibraryBatchSize, "Batch size for saving tracks (max 40)")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}

	reverseCfg.PlaylistIdentifier, err = reversePlaylist(reverseCfg.PlaylistIdentifier, positional)
	if err != nil {
		return err
	}

	return runReverse(ctx, configPath, reverseCfg, stdout, stderr)
}

// reversePlaylist picks the reverse source from the --playlist value and the
// positional arguments. Exactly one playlist may be named, in either form; both
// forms are accepted together only when they agree.
func reversePlaylist(flagged string, positional []string) (string, error) {
	switch {
	case len(positional) > 1:
		return "", fmt.Errorf("%w: reverse takes exactly one playlist, got %d arguments: %q",
			errUsage, len(positional), positional)
	case len(positional) == 1 && flagged != "" && positional[0] != flagged:
		return "", fmt.Errorf("%w: conflicting playlists: argument %q and --playlist %q",
			errUsage, positional[0], flagged)
	case len(positional) == 1:
		return positional[0], nil
	case flagged != "":
		return flagged, nil
	default:
		return "", fmt.Errorf("%w: %s", errUsage, reverseNeedsPlaylistMsg)
	}
}

// runReverse opens a session and adds the playlist named by reverseCfg to Liked Songs.
func runReverse(ctx context.Context, configPath string, reverseCfg sync.ReverseConfig, stdout, stderr io.Writer) error {
	s, err := openSession(ctx, configPath, stderr)
	if err != nil {
		return err
	}
	defer s.close(ctx)

	result, err := sync.Reverse(ctx, s.logger, s.client, s.store, reverseCfg)
	if err != nil {
		return fmt.Errorf("reverse sync failed: %w", err)
	}

	return writeSummary(stdout, reverseSummary{Success: true, ReverseResult: result})
}

// mergeSources combines the positional source playlists with the --source
// values, positional first, preserving order.
func mergeSources(positional, flagged []string) []string {
	return slices.Concat(positional, flagged)
}

// mergeCommand merges two or more playlists into a new playlist.
func mergeCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var (
		configPath string
		sources    stringList
		mergeCfg   sync.MergeConfig
	)

	fs := newFlagSet("merge", "merge <source1> <source2> [source3...] [flags]", stderr, &configPath)
	fs.Var(&sources, "source", "Source playlist (repeatable or comma-separated: --source p1 --source p2,p3)")
	fs.StringVar(&mergeCfg.TargetName, "name", "", "Name of the new merged playlist (required)")
	fs.StringVar(&mergeCfg.TargetName, "n", "", "Shorthand for --name")
	fs.BoolVar(&mergeCfg.Public, "public", true, "Make the new playlist public")
	fs.BoolVar(&mergeCfg.Deduplicate, "dedup", true, "Deduplicate tracks across all playlists")
	fs.BoolVar(&mergeCfg.DryRun, "dry-run", false,
		"Preview tracks that would be merged without creating the playlist")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}

	mergeCfg.Sources = mergeSources(positional, sources)

	if len(mergeCfg.Sources) < sync.MinMergeSources {
		return fmt.Errorf("%w: at least two source playlists are required: "+
			"provide them as arguments ('syncer merge <p1> <p2> <p3>') or with --source", errUsage)
	}

	if mergeCfg.TargetName == "" {
		return fmt.Errorf("%w: target playlist name is required (--name / -n)", errUsage)
	}

	s, err := openSession(ctx, configPath, stderr)
	if err != nil {
		return err
	}
	defer s.close(ctx)

	result, err := sync.MergePlaylists(ctx, s.logger, s.cfg, s.client, mergeCfg)
	if err != nil {
		return fmt.Errorf("merge failed: %w", err)
	}

	return writeSummary(stdout, mergeSummary{Success: true, MergeResult: result})
}
