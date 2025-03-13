package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/youtube/v3"
)

func (s *Service) InitialiseYoutubeAuthentication(ctx context.Context) error {

	// Read OAuth2 credentials from file
	// Create here: https://console.cloud.google.com/auth/clients?inv=1&invt=AbqgZQ&project=wildernessprime
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	oauth2Credentials, err := os.ReadFile(home + "/.config/wildernessprime/youtube-oauth2-client-secret.json")
	if err != nil {
		return fmt.Errorf("unable to read OAuth2 credentials file: %w", err)
	}

	config, err := google.ConfigFromJSON(
		oauth2Credentials,
		youtube.YoutubeReadonlyScope,
		"https://www.googleapis.com/auth/youtube.force-ssl",
	)
	if err != nil {
		return fmt.Errorf("unable to parse OAuth2 credentials file to config: %w", err)
	}

	token, err := getToken(config)
	if err != nil {
		return fmt.Errorf("unable to get token: %w", err)
	}

	client := config.Client(ctx, token)
	youtubeService, err := youtube.New(client)
	if err != nil {
		return fmt.Errorf("unable to create YouTube client: %w", err)
	}

	s.YoutubeService = youtubeService

	return nil
}

func getToken(config *oauth2.Config) (*oauth2.Token, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home dir: %w", err)
	}
	tokenFile := home + "/.config/wildernessprime/youtube-oauth2-refresh-token.json"

	token, err := tokenFromFile(tokenFile)
	if err == nil {
		return token, nil
	}

	codeCh := make(chan string)
	srv := &http.Server{Addr: "localhost:8080"}

	http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		codeCh <- code
		fmt.Fprintf(w, `
			<html>
			<head>
				<meta http-equiv="refresh" content="9999">
			</head>
			<body>
				Authorization code received. You can close this window.
			</body>
			</html>
		`)
		go func() {
			srv.Shutdown(context.Background())
		}()
	})

	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe(): %v", err)
		}
	}()

	config.RedirectURL = "http://localhost:8080/callback"

	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the authorization code: \n%v\n", authURL)

	code := <-codeCh
	token, err = config.Exchange(context.Background(), code)
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve token from web: %w", err)
	}

	if err := saveToken(tokenFile, token); err != nil {
		return nil, fmt.Errorf("unable to save token: %w", err)
	}

	return token, nil
}

func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	token := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(token)
	return token, err
}

func saveToken(file string, token *oauth2.Token) error {
	f, err := os.Create(file)
	if err != nil {
		return fmt.Errorf("unable to save token: %w", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
	return nil
}
