// This example demonstrates how to authenticate with Spotify using the authorization code flow.
// In order to run this example yourself, you'll need to:
//
//  1. Register an application at: https://developer.spotify.com/my-applications/
//     - Use "http://localhost:8080/callback" as the redirect URI
//  2. Set the SPOTIFY_ID environment variable to the client ID you got in step 1.
//  3. Set the SPOTIFY_SECRET environment variable to the client secret from step 1.
package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	mapset "github.com/deckarep/golang-set/v2"

	"github.com/pkg/browser"
	"github.com/spf13/viper"
	"github.com/zmb3/spotify"
)

const (
	Port               = "PORT"
	RedirectURI        = "REDIRECT_URI"
	ClientID           = "CLIENT_ID"
	ClientSecret       = "CLIENT_SECRET"
	PublicPlaylistName = "PUBLIC_PLAYLIST_NAME"
	BatchSize          = 100
	ReadTimeout        = 5 * time.Second
	WriteTimeout       = 10 * time.Second
	IdleTimeout        = 15 * time.Second
)

var (
	ch    = make(chan *spotify.Client)
	state = "abc123"
)

func getCompleteAuth(auth *spotify.Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, err := auth.Token(state, r)
		if err != nil {
			http.Error(w, "Couldn't get token", http.StatusForbidden)
			log.Fatal(err)
		}
		if st := r.FormValue("state"); st != state {
			http.NotFound(w, r)
			log.Fatalf("State mismatch: %s != %s\n", st, state)
		}
		// use the token to get an authenticated client
		client := auth.NewClient(tok)
		fmt.Fprintf(w, "Login Completed!")
		ch <- &client
	}
}

func getLikedSongsPlaylistID(client *spotify.Client, publicPlaylistName string) spotify.ID {
	playlistID := spotify.ID("")

	if playlists, err := client.CurrentUsersPlaylists(); err == nil {
		for _, playlist := range playlists.Playlists {
			if playlist.Name == publicPlaylistName {
				playlistID = playlist.ID
			}
		}
	} else {
		log.Printf("err: %v", err)
	}
	return playlistID
}

func getLikedSongIDs(client *spotify.Client) mapset.Set[spotify.ID] {
	likedSongs, err := client.CurrentUsersTracks()
	if err != nil {
		log.Fatal(err)
	}
	likedSongIDs := mapset.NewSet[spotify.ID]()
	hasNextPage := true

	for hasNextPage {
		for _, track := range likedSongs.Tracks {
			likedSongIDs.Add(track.ID)
		}
		if err = client.NextPage(likedSongs); errors.Is(err, spotify.ErrNoMorePages) {
			hasNextPage = false
		} else if err != nil {
			log.Fatal(err)
		}
	}
	return likedSongIDs
}

func getPlaylistSongIDs(client *spotify.Client, playlistID spotify.ID) mapset.Set[spotify.ID] {
	playlistTracks, err := client.GetPlaylistTracks(playlistID)
	if err != nil {
		log.Fatal(err)
	}

	playlistSongIDs := mapset.NewSet[spotify.ID]()
	hasNextPage := true

	for hasNextPage {
		for _, track := range playlistTracks.Tracks {
			playlistSongIDs.Add(track.Track.ID)
		}
		if err = client.NextPage(playlistTracks); errors.Is(err, spotify.ErrNoMorePages) {
			hasNextPage = false
		} else if err != nil {
			log.Fatal(err)
		}
	}

	return playlistSongIDs
}

func syncPublicPlaylistWithLikedSongs(
	client *spotify.Client,
	playlistID spotify.ID,
	playlistSongs mapset.Set[spotify.ID],
	likedSongs mapset.Set[spotify.ID],
) {
	log.Println("Begin Syncing")
	songsToAdd := likedSongs.Difference(playlistSongs).ToSlice()
	songsToRemove := playlistSongs.Difference(likedSongs).ToSlice()

	for i := 0; i < len(songsToAdd); i += BatchSize {
		end := i + BatchSize
		if end > len(songsToAdd) {
			end = len(songsToAdd)
		}

		batch := songsToAdd[i:end]
		_, err := client.AddTracksToPlaylist(playlistID, batch...)
		if err != nil {
			log.Printf("Error adding batch of songs: %v", err)
		}
	}

	for i := 0; i < len(songsToRemove); i += BatchSize {
		end := i + BatchSize
		if end > len(songsToRemove) {
			end = len(songsToRemove)
		}

		batch := songsToRemove[i:end]
		_, err := client.RemoveTracksFromPlaylist(playlistID, batch...)
		if err != nil {
			log.Printf("Error removing song: %v", err)
		}
	}
	log.Println("Sync Complete")
}

func setDefaults() {
	viper.AutomaticEnv()
	viper.SetDefault(Port, "8080")

	// redirectURI is the OAuth redirect URI for the application.
	// You must register an application at Spotify's developer portal
	// and enter this value.
	viper.SetDefault(RedirectURI, "http://localhost:8080/callback")
	viper.SetDefault(ClientID, "")
	viper.SetDefault(ClientSecret, "")
	viper.SetDefault(PublicPlaylistName, "Public Liked Songs")

	viper.SetConfigType("env")
	viper.AddConfigPath(".")
	viper.SetConfigFile(".env")
	err := viper.ReadInConfig()

	if err != nil {
		panic(fmt.Errorf("fatal error config file: %w", err))
	}
}

func main() {
	setDefaults()

	port := viper.GetString(Port)
	clientID := viper.GetString(ClientID)
	clientSecret := viper.GetString(ClientSecret)

	requiredScopes := []string{
		spotify.ScopeUserReadPrivate,
		spotify.ScopePlaylistReadPrivate,
		spotify.ScopeUserLibraryRead,
		spotify.ScopePlaylistModifyPublic,
		spotify.ScopePlaylistModifyPrivate,
	}
	auth := spotify.NewAuthenticator(
		viper.GetString(RedirectURI), requiredScopes...)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      nil,
		ReadTimeout:  ReadTimeout,
		WriteTimeout: WriteTimeout,
		IdleTimeout:  IdleTimeout,
	}
	http.HandleFunc("/callback", getCompleteAuth(&auth))
	http.HandleFunc("/", func(_ http.ResponseWriter, r *http.Request) {
		log.Println("Got request for:", r.URL.String())
	})

	go func(server *http.Server) {
		if err := server.ListenAndServe(); err != nil {
			log.Fatalf("failed to start server: %v", err)
		}
	}(server)

	auth.SetAuthInfo(clientID, clientSecret)
	url := auth.AuthURL(state)

	log.Printf("Please log in to Spotify by visiting the following page in your browser: %s", url)
	err := browser.OpenURL(url)
	if err != nil {
		log.Fatalf("could not open browser to login: %v", err)
	}

	// wait for auth to complete
	client := <-ch

	playlistID := getLikedSongsPlaylistID(client, viper.GetString(PublicPlaylistName))
	playlistSongs := getPlaylistSongIDs(client, playlistID)
	likedSongs := getLikedSongIDs(client)

	syncPublicPlaylistWithLikedSongs(client, playlistID, playlistSongs, likedSongs)
}
