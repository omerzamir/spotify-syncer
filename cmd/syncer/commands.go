package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"spotify-syncer/internal/auth"
	"spotify-syncer/internal/sync"
)

const (
	modeFull = "full"
	modeFast = "fast"

	// defaultLibraryBatchSize is Spotify's maximum number of tracks per
	// save-to-library request.
	defaultLibraryBatchSize = 50
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
	fs.StringVar(&mode, "mode", modeFull, "Sync mode: full or fast")
	fs.BoolVar(&enableGenres, "genres", true, "Enable genre playlist sync")
	fs.BoolVar(&forceRefresh, "force-refresh", false, "Force refresh of cached playlist contents")
	fs.BoolVar(&reverse, "reverse", false, "Reverse mode: add songs from playlist to liked songs")
	fs.StringVar(&playlist, "playlist", "",
		"Playlist name, ID, or URL for reverse mode (default: mirror_playlist_name from config)")
	fs.BoolVar(&dryRun, "dry-run", false, "Dry run mode (for reverse sync)")

	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}

	if reverse {
		return runReverse(ctx, configPath, sync.ReverseConfig{
			PlaylistIdentifier: playlist,
			BatchSize:          defaultLibraryBatchSize,
			DryRun:             dryRun,
		}, stdout, stderr)
	}

	if mode != modeFull && mode != modeFast {
		return fmt.Errorf("%w: invalid mode %q (must be %q or %q)", errUsage, mode, modeFull, modeFast)
	}

	s, err := openSession(ctx, configPath, stderr)
	if err != nil {
		return err
	}
	defer s.close(ctx)

	if mode == modeFast {
		s.logger.InfoContext(ctx, "fast mode not yet implemented, falling back to full mode")
	}

	result, err := sync.Run(ctx, s.logger, s.cfg, s.client, s.store, enableGenres, forceRefresh)
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

// reverseCommand adds every song from a playlist to Liked Songs.
func reverseCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var (
		configPath string
		reverseCfg sync.ReverseConfig
	)

	fs := newFlagSet("reverse", "reverse [flags]", stderr, &configPath)
	fs.StringVar(&reverseCfg.PlaylistIdentifier, "playlist", "",
		"Playlist name, ID, or URL (default: mirror_playlist_name from config)")
	fs.BoolVar(&reverseCfg.DryRun, "dry-run", false,
		"Preview tracks that would be added without actually adding them")
	fs.IntVar(&reverseCfg.BatchSize, "batch-size", defaultLibraryBatchSize, "Batch size for saving tracks (max 50)")

	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}

	return runReverse(ctx, configPath, reverseCfg, stdout, stderr)
}

func runReverse(ctx context.Context, configPath string, reverseCfg sync.ReverseConfig, stdout, stderr io.Writer) error {
	s, err := openSession(ctx, configPath, stderr)
	if err != nil {
		return err
	}
	defer s.close(ctx)

	result, err := sync.Reverse(ctx, s.logger, s.cfg, s.client, reverseCfg)
	if err != nil {
		return fmt.Errorf("reverse sync failed: %w", err)
	}

	return writeSummary(stdout, reverseSummary{Success: true, ReverseResult: result})
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

	mergeCfg.Sources = positional
	if len(mergeCfg.Sources) == 0 {
		mergeCfg.Sources = sources
	}

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
