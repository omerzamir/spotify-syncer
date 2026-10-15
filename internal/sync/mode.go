package sync

import "fmt"

// Mode selects how a sync run discovers liked tracks.
type Mode string

const (
	// ModeFull fetches every liked track and reconciles all playlists,
	// including removing tracks that are no longer liked.
	ModeFull Mode = "full"

	// ModeFast fetches only tracks liked since the last successful run and
	// adds them to the playlists. It never removes tracks; run full mode
	// periodically to clean up unliked tracks.
	ModeFast Mode = "fast"
)

// ParseMode parses a mode name as accepted on the command line.
func ParseMode(s string) (Mode, error) {
	switch mode := Mode(s); mode {
	case ModeFull, ModeFast:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid mode %q (must be %q or %q)", s, ModeFull, ModeFast)
	}
}
