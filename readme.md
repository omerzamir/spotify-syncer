# Spotify Syncer

A go project that syncs your liked/saved songs into a public playlist so that everyone can see an up-to-date playlist of your favorited songs.

## Getting Started

Create a unique clientID and clientSecret for your app located at https://developer.spotify.com/dashboard/. Add these new IDs to the app on `main.go:21` and `main.go:22`.
Add the redirectURI in the project `http://localhost:8080/callback` to the list of redirect URIs in the spotify application settings page. You may replace this if you like.
Create a new playlist that you would like to be synced with your liked songs. you do not have to create one if you already have a playlist you wish to sync. add the name of this playlist to the variable on `main.go:23`
**_important note: this app will add and remove items from the playlist, so be sure that you use either a new playlist or be completely ok with the existing playlist getting changed._**

After replacing the above variables, run the command `go build`.
run the new .exe and shablam
