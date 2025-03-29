package upload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/youtube/v3"
)

func (s *Service) InitialiseServiceAccount(ctx context.Context) error {

	// Create here: https://console.cloud.google.com/iam-admin/serviceaccounts/details/104677990570467761179/keys?inv=1&invt=AbqgZw&project=wildernessprime&supportedpurview=project
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	filePath := path.Join(home, ".config", "wildernessprime", "google-service-account-token.json")
	serviceAccountToken, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("unable to read service account file: %w", err)
	}

	serviceAccountConfig, err := google.JWTConfigFromJSON(
		serviceAccountToken,
		drive.DriveScope,
		"https://www.googleapis.com/auth/spreadsheets",
	)
	if err != nil {
		return fmt.Errorf("unable to parse service account file to config: %w", err)
	}
	s.ServiceAccountClient = serviceAccountConfig.Client(ctx)

	return nil
}

func (s *Service) InitialiseYoutubeAuthentication(ctx context.Context) error {

	// Read OAuth2 credentials from file
	// Create here: https://console.cloud.google.com/auth/clients?inv=1&invt=AbqgZQ&project=wildernessprime
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	filePath := path.Join(home, ".config", "wildernessprime", "youtube-oauth2-client-secret.json")
	oauth2Credentials, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("unable to read OAuth2 credentials file: %w", err)
	}

	config, err := google.ConfigFromJSON(
		oauth2Credentials,
		youtube.YoutubeUploadScope,
		"https://www.googleapis.com/auth/youtube.force-ssl",
	)
	if err != nil {
		return fmt.Errorf("unable to parse OAuth2 credentials file to config: %w", err)
	}

	token, err := getToken(ctx, config)
	if err != nil {
		return fmt.Errorf("unable to get token: %w", err)
	}

	client := config.Client(ctx, token)
	youtubeService, err := youtube.New(client)
	if err != nil {
		return fmt.Errorf("unable to create YouTube client: %w", err)
	}

	s.YoutubeService = youtubeService

	data := url.Values{}
	data.Set("client_id", config.ClientID)
	data.Set("client_secret", config.ClientSecret)
	data.Set("refresh_token", token.RefreshToken)
	data.Set("grant_type", "refresh_token")

	req, err := http.NewRequest("POST", "https://oauth2.googleapis.com/token", strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("creating new token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errorMessage, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to refresh token: %s", errorMessage)
	}

	var res map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return fmt.Errorf("parsing token response: %w", err)
	}

	accessToken, ok := res["access_token"].(string)
	if !ok {
		return errors.New("failed to extract access token")
	}

	s.YoutubeAccessToken = accessToken

	return nil
}

func getToken(ctx context.Context, config *oauth2.Config) (*oauth2.Token, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home dir: %w", err)
	}
	filePath := path.Join(home, ".config", "wildernessprime", "youtube-oauth2-refresh-token.json")

	token, err := tokenFromFile(filePath)
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
			srv.Shutdown(ctx)
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
	token, err = config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve token from web: %w", err)
	}

	if err := saveToken(filePath, token); err != nil {
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
