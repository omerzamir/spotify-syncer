package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"spotify-syncer/internal/auth"
	"spotify-syncer/internal/cache"
	"spotify-syncer/internal/config"
	"spotify-syncer/internal/spotifyio"
	"spotify-syncer/internal/sync"
)

const (
	exitSuccess = 0
	exitError   = 1
	exitConfig  = 2
)

func main() {
	rootCmd := setupCommands()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(exitError)
	}
}

func setupCommands() *cobra.Command {
	var configPath string

	rootCmd := &cobra.Command{
		Use:   "syncer",
		Short: "Spotify playlist syncer",
		Long:  "Syncs liked songs to mirror and genre playlists",
	}

	rootCmd.PersistentFlags().
		StringVar(&configPath, "config", "", "Path to config file (default: config.yaml in current directory)")

	authCmd := &cobra.Command{
		Use:   "auth",
		Short: "Perform interactive OAuth login",
		Long:  "Opens a browser for Spotify OAuth login and saves the token",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runAuth(configPath)
		},
	}

	var mode string
	var enableGenres bool
	var forceRefresh bool
	var isReverse bool
	var reversePlaylistFlag string
	var reverseDryRunFlag bool

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run the sync operation",
		Long:  "Fetches liked tracks, syncs mirror playlist, and optionally syncs genre playlists",
		RunE: func(_ *cobra.Command, _ []string) error {
			if isReverse {
				return runReverse(configPath, reversePlaylistFlag, reverseDryRunFlag, 50)
			}
			return runSync(configPath, mode, enableGenres, forceRefresh)
		},
	}

	runCmd.Flags().StringVar(&mode, "mode", "full", "Sync mode: full or fast")
	runCmd.Flags().BoolVar(&enableGenres, "genres", true, "Enable genre playlist sync")
	runCmd.Flags().BoolVar(&forceRefresh, "force-refresh", false, "Force refresh of cached playlist contents")
	runCmd.Flags().BoolVar(&isReverse, "reverse", false, "Reverse mode: add songs from playlist to liked songs")
	runCmd.Flags().StringVar(&reversePlaylistFlag, "playlist", "", "Playlist name, ID, or URL for reverse mode (default: mirror_playlist_name from config)")
	runCmd.Flags().BoolVar(&reverseDryRunFlag, "dry-run", false, "Dry run mode (for reverse sync)")

	var reversePlaylist string
	var reverseDryRun bool
	var reverseBatchSize int

	reverseCmd := &cobra.Command{
		Use:   "reverse",
		Short: "Add all songs from a playlist to liked songs",
		Long:  "Fetches tracks from a playlist (by name, ID, or URL) and saves them to your Liked Songs library",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runReverse(configPath, reversePlaylist, reverseDryRun, reverseBatchSize)
		},
	}

	reverseCmd.Flags().StringVar(&reversePlaylist, "playlist", "", "Playlist name, ID, or URL (default: mirror_playlist_name from config)")
	reverseCmd.Flags().BoolVar(&reverseDryRun, "dry-run", false, "Preview tracks that would be added without actually adding them")
	reverseCmd.Flags().IntVar(&reverseBatchSize, "batch-size", 50, "Batch size for saving tracks (max 50)")

	var mergeSourcesFlag []string
	var mergeTargetName string
	var mergePublic bool
	var mergeDedup bool
	var mergeDryRun bool

	mergeCmd := &cobra.Command{
		Use:   "merge <source1> <source2> [source3...] [flags]",
		Short: "Merge multiple playlists together into a new playlist",
		Long:  "Fetches tracks from two or more source playlists and creates a new playlist containing the combined tracks",
		RunE: func(_ *cobra.Command, args []string) error {
			sources := args
			if len(sources) == 0 {
				sources = mergeSourcesFlag
			}
			if len(sources) < 2 {
				return errors.New("at least two source playlists are required: provide as arguments ('syncer merge <p1> <p2> <p3>') or with --source")
			}
			if mergeTargetName == "" {
				return errors.New("target playlist name is required (--name / -n)")
			}
			return runMerge(configPath, sources, mergeTargetName, mergePublic, mergeDedup, mergeDryRun)
		},
	}

	mergeCmd.Flags().StringSliceVar(&mergeSourcesFlag, "source", nil, "Source playlist (can specify multiple times: --source p1 --source p2 --source p3)")
	mergeCmd.Flags().StringVarP(&mergeTargetName, "name", "n", "", "Name of the new merged playlist (required)")
	mergeCmd.Flags().BoolVar(&mergePublic, "public", true, "Make the new playlist public")
	mergeCmd.Flags().BoolVar(&mergeDedup, "dedup", true, "Deduplicate tracks across all playlists")
	mergeCmd.Flags().BoolVar(&mergeDryRun, "dry-run", false, "Preview tracks that would be merged without creating the playlist")

	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(reverseCmd)
	rootCmd.AddCommand(mergeCmd)

	return rootCmd
}

func runAuth(configPath string) error {
	cfg, logger := loadConfigAndLogger(configPath, true)

	ctx, cancel := setupContext()
	defer cancel()

	token, err := auth.InteractiveLogin(ctx, logger, cfg.Spotify)
	if err != nil {
		return fmt.Errorf("interactive login failed: %w", err)
	}

	store, err := cache.NewStore(cfg.StateDir, logger)
	if err != nil {
		return fmt.Errorf("creating cache store: %w", err)
	}

	if saveErr := store.SaveToken(token); saveErr != nil {
		return fmt.Errorf("saving token: %w", saveErr)
	}

	logger.InfoContext(ctx, "authentication successful, token saved")
	return nil
}

func runSync(configPath, mode string, enableGenres, forceRefresh bool) error {
	cfg, logger := loadConfigAndLogger(configPath, false)

	if mode != "full" && mode != "fast" {
		log.Printf("invalid mode: %s (must be 'full' or 'fast')", mode)
		return fmt.Errorf("invalid mode: %s (must be 'full' or 'fast')", mode)
	}

	store, err := cache.NewStore(cfg.StateDir, logger)
	if err != nil {
		return fmt.Errorf("creating cache store: %w", err)
	}

	token, err := store.LoadToken()
	if err != nil {
		return fmt.Errorf("loading token: %w", err)
	}

	if token == nil {
		log.Printf("no saved token found, run 'syncer auth' first")
		return errors.New("no saved token found, run 'syncer auth' first")
	}

	ctx, cancel := setupContextWithLogger(logger)
	defer cancel()

	tokenSource := auth.NewTokenSource(ctx, cfg.Spotify, token)
	client, err := spotifyio.NewClient(ctx, logger, cfg, tokenSource)
	if err != nil {
		return fmt.Errorf("creating Spotify client: %w", err)
	}

	updatedToken, err := tokenSource.Token()
	if err != nil {
		return fmt.Errorf("getting token: %w", err)
	}

	if updatedToken.AccessToken != token.AccessToken {
		if saveErr := store.SaveToken(updatedToken); saveErr != nil {
			logger.WarnContext(ctx, "failed to save refreshed token", "error", saveErr)
		}
	}

	if mode == "fast" {
		logger.InfoContext(ctx, "fast mode not yet implemented, falling back to full mode")
	}

	result, err := sync.Run(
		ctx,
		logger,
		cfg,
		client,
		store,
		enableGenres,
		forceRefresh,
	)
	if err != nil {
		return fmt.Errorf("sync run failed: %w", err)
	}

	summary := map[string]interface{}{
		"success":        result.Success,
		"liked_total":    result.Metrics.LikedTotal,
		"dedup_removed":  result.Metrics.DedupRemoved,
		"mirror_added":   result.Metrics.MirrorAdded,
		"mirror_removed": result.Metrics.MirrorRemoved,
		"duration_sec":   result.Metrics.FinishedAt.Sub(result.Metrics.StartedAt).Seconds(),
	}

	if enableGenres {
		summary["buckets_created"] = result.Metrics.BucketsCreated
		summary["buckets_removed"] = result.Metrics.BucketsRemoved
	}

	if len(result.Metrics.DuplicateIDs) > 0 {
		summary["duplicate_ids"] = result.Metrics.DuplicateIDs
	}

	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("marshaling summary: %w", err)
	}

	fmt.Fprintf(os.Stdout, "%s\n", summaryJSON)

	if !result.Success {
		return errors.New("sync run completed with errors")
	}

	return nil
}

func runReverse(configPath, playlistIdentifier string, dryRun bool, batchSize int) error {
	cfg, logger := loadConfigAndLogger(configPath, false)

	store, err := cache.NewStore(cfg.StateDir, logger)
	if err != nil {
		return fmt.Errorf("creating cache store: %w", err)
	}

	token, err := store.LoadToken()
	if err != nil {
		return fmt.Errorf("loading token: %w", err)
	}

	if token == nil {
		log.Printf("no saved token found, run 'syncer auth' first")
		return errors.New("no saved token found, run 'syncer auth' first")
	}

	ctx, cancel := setupContextWithLogger(logger)
	defer cancel()

	tokenSource := auth.NewTokenSource(ctx, cfg.Spotify, token)
	client, err := spotifyio.NewClient(ctx, logger, cfg, tokenSource)
	if err != nil {
		return fmt.Errorf("creating Spotify client: %w", err)
	}

	updatedToken, err := tokenSource.Token()
	if err != nil {
		return fmt.Errorf("getting token: %w", err)
	}

	if updatedToken.AccessToken != token.AccessToken {
		if saveErr := store.SaveToken(updatedToken); saveErr != nil {
			logger.WarnContext(ctx, "failed to save refreshed token", "error", saveErr)
		}
	}

	result, err := sync.Reverse(ctx, logger, cfg, client, sync.ReverseConfig{
		PlaylistIdentifier: playlistIdentifier,
		BatchSize:          batchSize,
		DryRun:             dryRun,
	})
	if err != nil {
		return fmt.Errorf("reverse sync failed: %w", err)
	}

	summary := map[string]interface{}{
		"success":       true,
		"playlist_name": result.PlaylistName,
		"playlist_id":   result.PlaylistID,
		"total_tracks":  result.TotalTracks,
		"already_liked": result.AlreadyLiked,
		"newly_added":   result.NewlyAdded,
		"dry_run":       result.DryRun,
		"duration_sec":  result.DurationSec,
	}

	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("marshaling summary: %w", err)
	}

	fmt.Fprintf(os.Stdout, "%s\n", summaryJSON)
	return nil
}

func runMerge(configPath string, sources []string, targetName string, public, dedup, dryRun bool) error {
	cfg, logger := loadConfigAndLogger(configPath, false)

	store, err := cache.NewStore(cfg.StateDir, logger)
	if err != nil {
		return fmt.Errorf("creating cache store: %w", err)
	}

	token, err := store.LoadToken()
	if err != nil {
		return fmt.Errorf("loading token: %w", err)
	}

	if token == nil {
		log.Printf("no saved token found, run 'syncer auth' first")
		return errors.New("no saved token found, run 'syncer auth' first")
	}

	ctx, cancel := setupContextWithLogger(logger)
	defer cancel()

	tokenSource := auth.NewTokenSource(ctx, cfg.Spotify, token)
	client, err := spotifyio.NewClient(ctx, logger, cfg, tokenSource)
	if err != nil {
		return fmt.Errorf("creating Spotify client: %w", err)
	}

	updatedToken, err := tokenSource.Token()
	if err != nil {
		return fmt.Errorf("getting token: %w", err)
	}

	if updatedToken.AccessToken != token.AccessToken {
		if saveErr := store.SaveToken(updatedToken); saveErr != nil {
			logger.WarnContext(ctx, "failed to save refreshed token", "error", saveErr)
		}
	}

	result, err := sync.MergePlaylists(ctx, logger, cfg, client, sync.MergeConfig{
		Sources:     sources,
		TargetName:  targetName,
		Public:      public,
		Deduplicate: dedup,
		DryRun:      dryRun,
	})
	if err != nil {
		return fmt.Errorf("merge failed: %w", err)
	}

	summary := map[string]interface{}{
		"success":            true,
		"sources":            result.Sources,
		"target_name":        result.TargetName,
		"total_tracks":       result.TotalTracks,
		"unique_tracks":      result.UniqueTracks,
		"duplicates_skipped": result.DuplicatesSkipped,
		"dry_run":            result.DryRun,
		"duration_sec":       result.DurationSec,
	}

	if result.TargetID != "" {
		summary["target_id"] = result.TargetID
	}

	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("marshaling summary: %w", err)
	}

	fmt.Fprintf(os.Stdout, "%s\n", summaryJSON)
	return nil
}

// loadConfigAndLogger loads configuration, validates credentials, and creates
// a logger. Exits if config loading or validation fails.
func loadConfigAndLogger(configPath string, useTextHandler bool) (config.Config, *slog.Logger) {
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Printf("error loading config: %v", err)
		os.Exit(exitConfig)
	}

	if validateErr := cfg.ValidateCredentials(); validateErr != nil {
		log.Printf("error validating credentials: %v", validateErr)
		os.Exit(exitConfig)
	}

	var handler slog.Handler
	if useTextHandler {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: parseLogLevel(cfg.LogLevel),
		})
	} else {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level: parseLogLevel(cfg.LogLevel),
		})
	}

	logger := slog.New(handler)
	return cfg, logger
}

// setupContext creates a context with cancellation and sets up signal handling.
func setupContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		cancel()
	}()

	return ctx, cancel
}

// setupContextWithLogger creates a context with cancellation and sets up
// signal handling with logging.
func setupContextWithLogger(logger *slog.Logger) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.InfoContext(ctx, "interrupt received, shutting down gracefully")
		cancel()
	}()

	return ctx, cancel
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
