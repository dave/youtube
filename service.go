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

const DO_SHEETS = true
const DO_YOUTUBE = true

type Service struct {
	SheetsService        *sheets.Service
	YoutubeService       *youtube.Service
	ServiceAccountClient *http.Client
	DriveService         *drive.Service
	Spreadsheet          *sheets.Spreadsheet
	Sheets               map[string]*Sheet
	Expeditions          map[string]*Expedition
	YoutubeVideos        map[string]*youtube.Video
}

func (s *Service) Init(ctx context.Context) error {

	s.Sheets = map[string]*Sheet{}
	s.Expeditions = map[string]*Expedition{}
	s.YoutubeVideos = map[string]*youtube.Video{}

	if err := s.InitialiseServiceAccount(ctx); err != nil {
		return fmt.Errorf("unable to initialise service account: %w", err)
	}

	if DO_SHEETS {

		if err := s.InitSheetsService(); err != nil {
			return fmt.Errorf("init sheets service: %w", err)
		}

		if err := s.GetSheetData("expedition"); err != nil {
			return fmt.Errorf("unable to get expedition sheet data: %w", err)
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

	if DO_YOUTUBE {

		if err := s.InitialiseYoutubeAuthentication(ctx); err != nil {
			return fmt.Errorf("unable to get videos: %w", err)
		}

		if err := s.GetVideosData(); err != nil {
			return fmt.Errorf("unable to get videos: %w", err)
		}

		if err := s.ParseVideosMetaData(); err != nil {
			return fmt.Errorf("unable to parse videos metadata: %w", err)
		}

	}

	if err := s.InitDriveService(); err != nil {
		return fmt.Errorf("unable to initialise drive service: %w", err)
	}

	if err := s.FindDriveFiles(); err != nil {
		return fmt.Errorf("unable to find drive files: %w", err)
	}

	if DO_YOUTUBE {

		if err := s.UpdateVideos(); err != nil {
			return fmt.Errorf("updating videos: %w", err)
		}

		if err := s.CreateVideos(); err != nil {
			return fmt.Errorf("creating videos: %w", err)
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
		sheets.SpreadsheetsReadonlyScope,
		drive.DriveReadonlyScope,
		//youtube.YoutubeReadonlyScope,
		//"https://www.googleapis.com/auth/youtube.force-ssl",
	)
	if err != nil {
		return fmt.Errorf("unable to parse service account file to config: %w", err)
	}
	s.ServiceAccountClient = serviceAccountConfig.Client(ctx)

	return nil
}
