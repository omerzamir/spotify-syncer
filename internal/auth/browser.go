package auth

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

// openBrowser opens url in the user's default browser using the platform's
// launcher. It returns once the launcher has started; the launcher process is
// reaped in the background.
func openBrowser(ctx context.Context, url string) error {
	name, args := browserCommand(runtime.GOOS, url)

	cmd := exec.CommandContext(ctx, name, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", name, err)
	}

	go func() { _ = cmd.Wait() }()

	return nil
}

// browserCommand returns the launcher command for goos.
func browserCommand(goos, url string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}
