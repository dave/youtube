package upload

import (
	"context"
	"fmt"
	"net/http"

	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/sheets/v4"
	"google.golang.org/api/youtube/v3"
)

type Service struct {
	Global               *Global
	StorageService       StorageServices
	ChannelId            string
	SheetsService        *sheets.Service
	YoutubeService       *youtube.Service
	YoutubeAccessToken   string
	ServiceAccountClient *http.Client
	DriveService         *drive.Service
	DropboxConfig        *dropbox.Config
	Spreadsheet          *sheets.Spreadsheet
	Sheets               map[string]*Sheet
	Expeditions          map[string]*Expedition
	YoutubePlaylists     map[string]*youtube.Playlist
	VideoPreviewData     map[*Item]map[string]any
	PlaylistPreviewData  map[HasPlaylist]map[string]any
}

func New(channelId string) *Service {

	s := &Service{}
	s.StorageService = DropboxStorage
	s.Sheets = map[string]*Sheet{}
	s.Expeditions = map[string]*Expedition{}
	s.YoutubePlaylists = map[string]*youtube.Playlist{}
	s.VideoPreviewData = map[*Item]map[string]any{}
	s.PlaylistPreviewData = map[HasPlaylist]map[string]any{}

	s.ChannelId = channelId

	return s
}

func (s *Service) Start(ctx context.Context) error {

	if s.ChannelId == "" {
		return fmt.Errorf("channel id is empty, use NewService to create a new *Service")
	}

	// INITIALISE AUTHENTICATION
	{
		if err := s.InitialiseServiceAccount(ctx); err != nil {
			return fmt.Errorf("init service account: %w", err)
		}

		if err := s.InitialiseYoutubeAuthentication(ctx); err != nil {
			return fmt.Errorf("init youtube auth: %w", err)
		}

		if err := s.InitGoogleDriveService(); err != nil {
			return fmt.Errorf("init drive service: %w", err)
		}

		if err := s.InitDropboxService(ctx); err != nil {
			return fmt.Errorf("init dropbox service: %w", err)
		}

		if err := s.InitSheetsService(); err != nil {
			return fmt.Errorf("init sheets service: %w", err)
		}
	}

	// RESUME UPLOADER
	{
		if err := s.ResumePartialUpload(ctx); err != nil {
			return fmt.Errorf("unable to resume upload: %w", err)
		}
	}

	// GET SHEET DATA
	{
		if err := s.GetSheetData(nil, "global", "expedition"); err != nil {
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

	// CLEAR PREVIEW SHEET AND FOLDER
	{
		if err := s.ClearPreviewSheets(); err != nil {
			return fmt.Errorf("unable to clear preview sheet: %w", err)
		}
		if err := s.ClearGoogleDrivePreviewFolder(); err != nil {
			return fmt.Errorf("unable to clear preview folder: %w", err)
		}
		if err := s.ClearDropboxPreviewFolder(); err != nil {
			return fmt.Errorf("unable to clear dropbox preview folder: %w", err)
		}
	}

	// GENERATE AI TITLES
	{
		if err := s.GenerateAiTitles(ctx); err != nil {
			return fmt.Errorf("generating ai titles: %w", err)
		}
	}

	// GET DATA FROM YOUTUBE
	{
		if err := s.GetVideosData(); err != nil {
			return fmt.Errorf("unable to get videos: %w", err)
		}

		if err := s.GetVideosCaptions(); err != nil {
			return fmt.Errorf("unable to get captions: %w", err)
		}

		if err := s.GetPlaylistsData(); err != nil {
			return fmt.Errorf("unable to get playlists: %w", err)
		}
	}

	// GET DATA FROM DRIVE
	{
		if err := s.FindGoogleDriveFiles(); err != nil {
			return fmt.Errorf("unable to find drive files: %w", err)
		}
		if err := s.FindDropboxFiles(); err != nil {
			return fmt.Errorf("unable to find dropbox files: %w", err)
		}
	}

	// UPLOAD TO YOUTUBE
	{
		if err := s.CreateOrUpdateVideos(ctx); err != nil {
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

	// UPDATE THUMBNAILS
	{
		if err := s.UpdateThumbnails(); err != nil {
			return fmt.Errorf("updating thumbnails: %w", err)
		}
	}

	return nil
}

type StorageServices int

const (
	GoogleDriveStorage StorageServices = 1
	DropboxStorage     StorageServices = 2
)
