package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/sheets/v4"
	"google.golang.org/api/youtube/v3"
)

type Service struct {
	Global               *Global
	SheetsService        *sheets.Service
	YoutubeService       *youtube.Service
	YoutubeAccessToken   string
	ServiceAccountClient *http.Client
	DriveService         *drive.Service
	Spreadsheet          *sheets.Spreadsheet
	Sheets               map[string]*Sheet
	Expeditions          map[string]*Expedition
	YoutubeVideos        map[string]*youtube.Video
	YoutubePlaylists     map[string]*youtube.Playlist
	VideoPreviewData     map[*Item]map[string]any
	PlaylistPreviewData  map[HasPlaylist]map[string]any
}

func (s *Service) Init(ctx context.Context) error {

	s.Sheets = map[string]*Sheet{}
	s.Expeditions = map[string]*Expedition{}
	s.YoutubeVideos = map[string]*youtube.Video{}
	s.YoutubePlaylists = map[string]*youtube.Playlist{}
	s.VideoPreviewData = map[*Item]map[string]any{}
	s.PlaylistPreviewData = map[HasPlaylist]map[string]any{}

	// INITIALISE AUTHENTICATION
	{
		if err := s.InitialiseServiceAccount(ctx); err != nil {
			return fmt.Errorf("unable to initialise service account: %w", err)
		}

		if err := s.InitialiseYoutubeAuthentication(ctx); err != nil {
			return fmt.Errorf("unable to get videos: %w", err)
		}

		if err := s.InitDriveService(); err != nil {
			return fmt.Errorf("unable to initialise drive service: %w", err)
		}

		if err := s.InitSheetsService(); err != nil {
			return fmt.Errorf("init sheets service: %w", err)
		}
	}

	// RESUME UPLOADER
	{
		if err := s.ResumePartialUpload(); err != nil {
			return fmt.Errorf("unable to resume upload: %w", err)
		}
	}

	// GET SHEET DATA
	{
		if err := s.GetSheetData("global", "expedition"); err != nil {
			return fmt.Errorf("unable to get global / expedition sheet data: %w", err)
		}

		if err := s.ParseGlobal(); err != nil {
			return fmt.Errorf("unable to parse global: %w", err)
		}

		if err := s.ParseExpeditions(); err != nil {
			return fmt.Errorf("unable to parse expeditions: %w", err)
		}

		if err := s.GetAllSheetsData(); err != nil {
			return fmt.Errorf("unable to get sheets data: %w", err)
		}

		if err := s.ParseSections(); err != nil {
			return fmt.Errorf("unable to parse sections: %w", err)
		}

		if err := s.ParseItems(); err != nil {
			return fmt.Errorf("unable to parse items: %w", err)
		}

		if err := s.ParseTemplates(); err != nil {
			return fmt.Errorf("unable to parse templates: %w", err)
		}

		if err := s.ParseLinkedData(); err != nil {
			return fmt.Errorf("unable to parse linked data: %w", err)
		}
	}

	// GET DATA FROM YOUTUBE
	{
		if err := s.GetVideosData(); err != nil {
			return fmt.Errorf("unable to get videos: %w", err)
		}

		if err := s.ParseVideosMetaData(); err != nil {
			return fmt.Errorf("unable to parse videos metadata: %w", err)
		}

		if err := s.GetPlaylistsData(); err != nil {
			return fmt.Errorf("unable to get playlists: %w", err)
		}

		if err := s.ParsePlaylistsMetaData(); err != nil {
			return fmt.Errorf("unable to parse playlists metadata: %w", err)
		}
	}

	// GET DATA FROM DRIVE
	{
		if err := s.FindDriveFiles(); err != nil {
			return fmt.Errorf("unable to find drive files: %w", err)
		}
	}

	// UPLOAD TO YOUTUBE
	{
		if err := s.CreateOrUpdateVideos(); err != nil {
			return fmt.Errorf("updating videos: %w", err)
		}

		if err := s.CreateOrUpdatePlaylists(); err != nil {
			return fmt.Errorf("updating playlists: %w", err)
		}
	}

	// WRITE PREVIEW DATA TO SHEET
	{

		if err := s.WriteVideosPreview(); err != nil {
			return fmt.Errorf("unable to write videos preview: %w", err)
		}

		if err := s.WritePlaylistsPreview(); err != nil {
			return fmt.Errorf("unable to write playlists preview: %w", err)
		}
	}

	return nil
}

func (s *Service) InitialiseServiceAccount(ctx context.Context) error {

	// Create here: https://console.cloud.google.com/iam-admin/serviceaccounts/details/104677990570467761179/keys?inv=1&invt=AbqgZw&project=wildernessprime&supportedpurview=project
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	serviceAccountToken, err := os.ReadFile(home + "/.config/wildernessprime/google-service-account-token.json")
	if err != nil {
		return fmt.Errorf("unable to read service account file: %w", err)
	}

	serviceAccountConfig, err := google.JWTConfigFromJSON(
		serviceAccountToken,
		drive.DriveReadonlyScope,
		"https://www.googleapis.com/auth/spreadsheets",
	)
	if err != nil {
		return fmt.Errorf("unable to parse service account file to config: %w", err)
	}
	s.ServiceAccountClient = serviceAccountConfig.Client(ctx)

	return nil
}
