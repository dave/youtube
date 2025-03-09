package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"golang.org/x/oauth2"
)

func getToken(config *oauth2.Config) (*oauth2.Token, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home dir: %w", err)
	}
	tokenFile := home + "/.config/wildernessprime/refresh_token.txt"

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
