# Spotify Syncer

A go project that syncs your liked/saved songs into a public playlist so that everyone can see an up-to-date playlist of your favorited songs.

## Getting Started

Create a unique clientID and clientSecret for your app located at https://developer.spotify.com/dashboard/.
Create a `.env` file with the following values:
```
CLIENT_ID=<Put your ClientID here>
CLIENT_SECRET=<Put your Client Secret here>
PUBLIC_PLAYLIST_NAME=<Put your public liked songs playlist here>
```

Add the redirectURI in the project `http://127.0.0.1:8080/callback` to the list of redirect URIs in the spotify application settings page. You may replace this if you like.

Create a new playlist that you would like to be synced with your liked songs. you do not have to create one if you already have a playlist you wish to sync. add the name of this playlist to the variable on your `.env` file.

**_important note: this app will add and remove items from the playlist, so be sure that you use either a new playlist or be completely ok with the existing playlist getting changed._**

After replacing the above variables, run the command `go build`.
run the new executable and you got it, your playlists are synced.
