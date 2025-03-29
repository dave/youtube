package upload

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/dave/youtube/resume"
)

func (s *Service) ResumePartialUpload(ctx context.Context) error {

	res, err := s.getResume()
	if err != nil {
		return fmt.Errorf("getting uploader: %w", err)
	}

	if res.State == resume.StateUploadInProgress {
		progress := func(start int64) {
			fmt.Printf(" - uploaded %d of %d bytes (%.2f%%)\n", start, res.ContentLength, float64(start)/float64(res.ContentLength)*100)
		}
		fmt.Println("Unfinished upload found... resuming:")
		video, err := res.Upload(ctx, progress)
		if err != nil {
			return fmt.Errorf("unable to upload: %w", err)
		}
		fmt.Println("Upload finished", video.Id)
	}

	return nil
}

func (s *Service) getResume() (*resume.Service, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home dir: %w", err)
	}
	filePath := path.Join(home, ".config", "wildernessprime", "uploader-state.json")

	var res *resume.Service
	switch s.StorageService {
	case GoogleDriveStorage:
		res, err = resume.NewGoogleDrive(
			s.DriveService,
			s.YoutubeAccessToken,
			1024*1024*16, // 16MB
			filePath,
		)
		if err != nil {
			return nil, fmt.Errorf("initialising google drive resumer: %w", err)
		}
	case DropboxStorage:
		res, err = resume.NewDropbox(
			s.DropboxConfig,
			s.YoutubeAccessToken,
			1024*1024*16, // 16MB
			filePath,
		)
		if err != nil {
			return nil, fmt.Errorf("initialising dropbox resumer: %w", err)
		}
	}

	return res, nil

}
