# Spotify Syncer

A Go CLI tool that syncs your liked/saved songs into playlists:
- **Mirror playlist**: A public playlist mirroring all your liked songs
- **Genre playlists**: Automatically organized playlists by genre (optional)

## Features

- **ISRC-based deduplication**: Removes duplicate tracks (single vs album versions)
- **Dynamic genre discovery**: Automatically creates genre playlists based on your library
- **Aggressive caching**: Minimizes API calls by caching artist genres, playlist contents, and track metadata
- **Rate limiting**: Built-in rate limiting with automatic retry on 429 responses
- **Future-proof API usage**: Uses the endpoints Spotify introduced in February 2026 (`/playlists/{id}/items`, `/me/playlists`, `/me/library`) and falls back to the older ones per request when a new endpoint is unavailable, for example when reading a playlist you follow but do not own
- **Reverse sync**: Add all songs from any playlist (by name, ID, or URL) into your Liked Songs library
- **Playlist merge**: Merge two or more playlists into a new playlist with automatic track deduplication
- **Fast mode**: Incremental sync that only adds songs liked since the last run (see [Sync modes](#sync-modes))

## Getting Started

### 1. Create a Spotify App

1. Go to https://developer.spotify.com/dashboard/
2. Create a new app
3. Add `http://127.0.0.1:8080/callback` as a redirect URI

### 2. Configure Credentials

Set environment variables:

```bash
export SPOTIFY_CLIENT_ID=your_client_id
export SPOTIFY_CLIENT_SECRET=your_client_secret
```

### 3. Create Configuration File

Copy `config.example.yaml` to `config.yaml`:

```bash
cp config.example.yaml config.yaml
```

Edit `config.yaml` to adjust settings like playlist names, genre thresholds, etc. Unknown keys are rejected, so a typo fails loudly instead of being ignored. Credentials can also live in a `.env` file in the working directory; variables already set in the environment take precedence.

### 4. Build

```bash
go build -o syncer ./cmd/syncer
```

### 5. Authenticate

Run the interactive OAuth login:

```bash
./syncer auth
```

This will:
- Open your browser for Spotify login
- Save the OAuth token to `state/tokens.json`

### 6. Run Sync

Sync your liked songs:

```bash
./syncer run
```

With options:

```bash
# Only add songs liked since the last run (much faster on large libraries)
./syncer run --mode=fast

# Disable genre playlists
./syncer run --genres=false

# Force refresh cached playlist contents
./syncer run --force-refresh

# Use a custom config file
./syncer run --config=/path/to/config.yaml
```

### Sync modes

| | `full` (default) | `fast` |
|---|---|---|
| Liked Songs fetched | All of them | Only those liked since the last successful run (usually one request) |
| New likes | Added | Added |
| Unliked songs | Removed from the mirror and genre playlists | **Kept** until the next full run |
| Same song on single and album | Keeps the best release (playable, then most recently liked) and swaps it in if it changes | Skips a new release of a song that is already mirrored |
| Artist genres | Re-fetched once older than `artist_genre_ttl` | Only fetched for artists never seen before |

The syncer stores a checkpoint (the newest *liked at* time it has processed) in `state/checkpoints.json` and only advances it after a run succeeds, so a failed run is simply retried next time. Fast mode falls back to a full sync when it has no state yet (first run, or after deleting `state/`) and when `--force-refresh` is given.

Recommended schedule: run `fast` often (for example every few hours) and `full` occasionally (for example weekly) to clean up unliked songs.

The mirror playlist lists songs oldest-liked first, so new likes are appended at the end in both modes.

## Commands

### `syncer auth`

Performs interactive OAuth login and saves the token for future runs.

**Flags:**
- `--config`: Path to config file (default: `config.yaml` in current directory)

### `syncer run`

Runs the sync operation: fetches liked tracks, syncs mirror playlist, and optionally syncs genre playlists.

**Flags:**
- `--mode`: Sync mode - `full` or `fast` (default: `full`); see [Sync modes](#sync-modes)
- `--genres`: Enable genre playlist sync (default: `true`)
- `--force-refresh`: Force refresh of cached playlist contents (default: `false`)
- `--reverse`: Reverse mode - add songs from playlist to liked songs (default: `false`)
- `--playlist`: Target playlist name, ID, or URL for reverse mode (default: `mirror_playlist_name` from config)
- `--dry-run`: Dry-run mode for reverse sync (default: `false`)
- `--config`: Path to config file (default: `config.yaml` in current directory)

**Exit Codes:**
- `0`: Success
- `1`: Runtime error
- `2`: Usage, configuration or credential error

**Output:**

The `run` command outputs a JSON summary to stdout:

```json
{
  "success": true,
  "mode": "full",
  "liked_total": 5000,
  "dedup_removed": 15,
  "mirror_added": 5,
  "mirror_removed": 2,
  "buckets_created": 8,
  "buckets_removed": 0,
  "duration_sec": 45.2,
  "duplicate_ids": ["abc123", "def456"]
}
```

`mode` is the mode that actually ran (`full` when fast mode fell back). Fast runs also report `new_tracks`, the number of liked songs not seen before; `liked_total` is then the number of liked songs known from previous runs. Bucket counts are omitted when genre sync is disabled.

### `syncer reverse`

Adds all songs from a playlist (by name, ID, or URL) into your Liked Songs library. Tracks that are already liked are skipped to preserve their original added date. Matching uses ISRC codes, so a song you already liked as a single is not liked again from its album, and a song appearing twice in the playlist is only liked once.

After the first `syncer run`, reverse only fetches the songs liked since that run instead of your whole library.

**Flags:**
- `--playlist`: Playlist name, Spotify ID, or Spotify URL (default: `mirror_playlist_name` from config)
- `--dry-run`: Preview tracks that would be added without making changes (default: `false`)
- `--batch-size`: Batch size for saving tracks (1-40, default: `40`)
- `--config`: Path to config file (default: `config.yaml` in current directory)

**Output:**

```json
{
  "success": true,
  "playlist_name": "Public Liked Songs",
  "playlist_id": "37i9dQZF1DXcBWIGoYBM5M",
  "total_tracks": 150,
  "duplicates_skipped": 2,
  "already_liked": 120,
  "newly_added": 30,
  "dry_run": false,
  "duration_sec": 3.4
}
```

### `syncer merge`

Merges tracks from two or more source playlists into a newly created playlist. Source playlists can be passed as playlist names, Spotify IDs, Spotify URIs, or Spotify URLs. Tracks are automatically deduplicated across all playlists (unless disabled with `--dedup=false`), including different releases of the same song (matched by ISRC). The first occurrence wins and playlist order is preserved.

**Usage:**

```bash
syncer merge <source1> <source2> [source3...] --name "Merged Playlist"
```

**Flags:**
- `-n, --name`: Name of the new merged playlist (required)
- `--public`: Make the new playlist public (default: `true`)
- `--dedup`: Deduplicate tracks across all playlists (default: `true`)
- `--dry-run`: Preview tracks that would be merged without creating the playlist (default: `false`)
- `--source`: Specify a source playlist (repeatable or comma-separated, as an alternative to positional arguments)
- `--config`: Path to config file (default: `config.yaml` in current directory)

**Output:**

```json
{
  "success": true,
  "sources": [
    { "name": "Playlist 1", "id": "37i9dQZF1DXcBWIGoYBM5M", "tracks": 50 },
    { "name": "Playlist 2", "id": "4aawyAB9vmqN3uQFRjYTkK", "tracks": 40 },
    { "name": "Playlist 3", "id": "2A1...", "tracks": 35 }
  ],
  "target_name": "Merged Trilogy",
  "target_id": "7Y2...",
  "total_tracks": 125,
  "unique_tracks": 110,
  "duplicates_skipped": 15,
  "dry_run": false,
  "duration_sec": 2.45
}
```



## Cron Integration

Automate syncer runs using cron on macOS. The recommended approach is to use the provided wrapper script which handles environment variables and logging automatically.

### Quick Setup (Every Two Days)

1. **Edit your crontab:**
   ```bash
   crontab -e
   ```

2. **Add the following line** (replace `/path/to/spotify-syncer` with your actual project path):
   ```bash
   0 11 */2 * * /path/to/spotify-syncer/scripts/cron-run.sh
   ```

   This runs every two days at 11:00 AM. To customize the time, change `11` to your desired hour (0-23).

3. **Save and exit** (in `vi`, press `Esc`, type `:wq`, then press `Enter`)

The syncer will now run automatically every two days at 11:00 AM in fast (incremental) mode. Fast mode never removes songs you unliked, so also schedule an occasional full run, for example every Sunday at 4:00 AM:

```bash
0 4 * * 0 cd /path/to/spotify-syncer && ./bin/syncer run --mode=full >> logs/syncer-cron.log 2>&1
```

### Understanding Cron Syntax

The cron syntax is: `minute hour day-of-month month day-of-week`

- `0 11 */2 * *` - Runs at 11:00 AM every 2 days (recommended)
- `0 0 */2 * *` - Runs at midnight (00:00) every 2 days
- `0 3 * * *` - Runs daily at 3:00 AM
- `0 0 1,15 * *` - Runs on the 1st and 15th of each month at midnight
- `30 14 * * 0` - Runs every Sunday at 2:30 PM

### Using the Wrapper Script

The `scripts/cron-run.sh` script automatically:
- Loads environment variables from `.env` file
- Changes to the project directory
- Builds the binary if it doesn't exist
- Runs syncer in fast mode
- Logs output to `logs/syncer-cron.log` with timestamps

**Requirements:**
- `.env` file must exist in the project root with `SPOTIFY_CLIENT_ID` and `SPOTIFY_CLIENT_SECRET`
- The script must be executable (already set by default)

### Manual Cron Entry (Advanced)

If you prefer not to use the wrapper script, you can add a direct cron entry:

```bash
0 11 */2 * * cd /path/to/spotify-syncer && export $(grep -v '^#' .env | xargs) && ./bin/syncer run --mode=fast >> /path/to/spotify-syncer/logs/syncer.log 2>&1
```

**Important considerations:**
- Use absolute paths (cron runs with minimal PATH)
- Environment variables must be explicitly exported
- Ensure the log directory exists or redirect to a writable location

### Troubleshooting

**Cron job not running:**
1. **Check cron service:** On macOS, cron runs automatically. Verify it's enabled:
   ```bash
   sudo launchctl list | grep cron
   ```

2. **Check cron logs:** macOS cron logs to system logs. View with:
   ```bash
   log show --predicate 'process == "cron"' --last 1h
   ```

3. **Test the script manually:**
   ```bash
   /path/to/spotify-syncer/scripts/cron-run.sh
   ```
   Check `logs/syncer-cron.log` for errors.

**Environment variables not loaded:**
- Ensure `.env` file exists in the project root
- Verify `.env` file doesn't have syntax errors (no spaces around `=`)
- Check that credentials are not placeholders (`id`/`secret`)

**Permission errors:**
- Ensure the script is executable: `chmod +x scripts/cron-run.sh`
- Verify the log directory is writable: `mkdir -p logs && chmod 755 logs`
- Check that the binary exists or can be built: `make build`

**PATH issues:**
- Cron runs with a minimal PATH (`/usr/bin:/bin`)
- The wrapper script uses absolute paths to avoid PATH issues
- If using direct cron entry, use full paths to all binaries

**Viewing logs:**
- Wrapper script logs: `tail -f logs/syncer-cron.log`
- Check last run: `tail -n 50 logs/syncer-cron.log`
- Monitor in real-time: `tail -f logs/syncer-cron.log`

### Alternative: Using launchd (macOS)

For more advanced scheduling on macOS, consider using `launchd` instead of cron. Create a plist file at `~/Library/LaunchAgents/com.spotify-syncer.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.spotify-syncer</string>
    <key>ProgramArguments</key>
    <array>
        <string>/path/to/spotify-syncer/scripts/cron-run.sh</string>
    </array>
    <key>StartInterval</key>
    <integer>172800</integer>
    <key>RunAtLoad</key>
    <false/>
    <key>StandardOutPath</key>
    <string>/path/to/spotify-syncer/logs/launchd.log</string>
    <key>StandardErrorPath</key>
    <string>/path/to/spotify-syncer/logs/launchd-error.log</string>
</dict>
</plist>
```

Load with: `launchctl load ~/Library/LaunchAgents/com.spotify-syncer.plist`

## Configuration

See `config.example.yaml` for all available configuration options. Key settings:

- **Spotify**: OAuth redirect URI and port
- **Sync**: Mirror playlist name, concurrency, batch size
- **Genre**: Max buckets, min bucket size, genres per artist, playlist prefix
- **Rate Limit**: Requests per second, burst, max retries
- **State Directory**: Where to store cache files (default: `./state`)

## State Files

The syncer stores state in the `state/` directory (configurable via `state_dir`):

- `tokens.json`: OAuth2 tokens (access + refresh)
- `artist_genres.json`: Cached artist genre tags (30-day TTL)
- `track_artists.json`: Track-to-artist mappings (immutable)
- `track_isrc.json`: Track-to-ISRC mappings (immutable)
- `mirror_tracks.json`: Cached mirror playlist contents
- `bucket_tracks.json`: Cached genre playlist contents
- `playlists.json`: Playlist name-to-ID mappings
- `metrics_last.json`: Last run summary

**Important**: The `state/` directory is gitignored. Do not commit it.

## How It Works

1. **Fetch Liked Tracks**: Paginates through all liked songs
2. **Deduplicate**: Groups tracks by ISRC, keeps one representative per group
3. **Sync Mirror**: Computes diff between liked tracks and mirror playlist, applies changes
4. **Fetch Artist Genres**: Loads cached genres, fetches missing/expired artists
5. **Classify**: Assigns tracks to genre buckets using dynamic discovery
6. **Sync Genre Playlists**: Computes diff per bucket, applies changes

## License

MIT
