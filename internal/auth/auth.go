// Package auth provides OAuth2 authentication flows for the Spotify API,
// supporting both interactive login and automatic token refresh.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	spotifyauth "github.com/zmb3/spotify/v2/auth"
	"golang.org/x/oauth2"

	"spotify-syncer/internal/config"
)

const (
	stateBytes      = 16
	readTimeout     = 5 * time.Second
	writeTimeout    = 10 * time.Second
	idleTimeout     = 15 * time.Second
	shutdownTimeout = 5 * time.Second
)

type tokenResult struct {
	token *oauth2.Token
	err   error
}

// InteractiveLogin performs the OAuth2 authorization code flow by starting a
// local HTTP server on the configured port and opening the user's browser to
// the Spotify login page. It blocks until the user completes the login or the
// context is cancelled.
func InteractiveLogin(
	ctx context.Context,
	logger *slog.Logger,
	cfg config.SpotifyConfig,
) (*oauth2.Token, error) {
	state, err := generateState()
	if err != nil {
		return nil, err
	}

	authenticator := newAuthenticator(cfg)
	tokenCh := make(chan tokenResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", callbackHandler(authenticator, state, tokenCh))

	addr := fmt.Sprintf(":%d", cfg.Port)
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}

	lc := net.ListenConfig{}

	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", addr, err)
	}

	go func() {
		if serveErr := server.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case tokenCh <- tokenResult{err: fmt.Errorf("HTTP server error: %w", serveErr)}:
			default:
			}
		}
	}()

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	authURL := authenticator.AuthURL(state)
	logger.InfoContext(ctx, "please visit this URL to log in to Spotify", "url", authURL)

	if browserErr := openBrowser(ctx, authURL); browserErr != nil {
		logger.WarnContext(ctx, "could not open browser automatically, please visit the URL manually",
			"error", browserErr)
	}

	select {
	case result := <-tokenCh:
		return result.token, result.err
	case <-ctx.Done():
		return nil, fmt.Errorf("login cancelled: %w", ctx.Err())
	}
}

// NewTokenSource creates an auto-refreshing OAuth2 token source from a
// persisted token. The returned TokenSource transparently refreshes the
// access token when it expires. Call Token() after the run completes to
// retrieve the (possibly refreshed) token for persistence.
func NewTokenSource(
	ctx context.Context,
	cfg config.SpotifyConfig,
	token *oauth2.Token,
) oauth2.TokenSource {
	return oauthConfig(cfg).TokenSource(ctx, token)
}

func newAuthenticator(cfg config.SpotifyConfig) *spotifyauth.Authenticator {
	return spotifyauth.New(
		spotifyauth.WithRedirectURL(cfg.RedirectURI),
		spotifyauth.WithScopes(requiredScopes()...),
		spotifyauth.WithClientID(cfg.ClientID),
		spotifyauth.WithClientSecret(cfg.ClientSecret),
	)
}

func oauthConfig(cfg config.SpotifyConfig) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURI,
		Scopes:       requiredScopes(),
		Endpoint: oauth2.Endpoint{
			AuthURL:  spotifyauth.AuthURL,
			TokenURL: spotifyauth.TokenURL,
		},
	}
}

func requiredScopes() []string {
	return []string{
		spotifyauth.ScopeUserReadPrivate,
		spotifyauth.ScopePlaylistReadPrivate,
		spotifyauth.ScopeUserLibraryRead,
		spotifyauth.ScopeUserLibraryModify,
		spotifyauth.ScopePlaylistModifyPublic,
		spotifyauth.ScopePlaylistModifyPrivate,
	}
}

func generateState() (string, error) {
	b := make([]byte, stateBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating OAuth state: %w", err)
	}

	return hex.EncodeToString(b), nil
}

func callbackHandler(
	auth *spotifyauth.Authenticator,
	state string,
	ch chan<- tokenResult,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, err := auth.Token(r.Context(), state, r)
		if err != nil {
			http.Error(w, "Authentication failed", http.StatusForbidden)
			ch <- tokenResult{err: fmt.Errorf("exchanging auth code: %w", err)}

			return
		}

		fmt.Fprint(w, "Login completed! You can close this window.")
		ch <- tokenResult{token: tok}
	}
}
