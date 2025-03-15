package uploader

import (
	"context"
	"fmt"
	"os"
)

const CHUNK_SIZE = 1024 * 1024 * 16

func (s *Service) ResumePartialUpload(ctx context.Context) error {

	upl, err := s.getUploader()
	if err != nil {
		return fmt.Errorf("getting uploader: %w", err)
	}

	if upl.State == resumer.StateUploadInProgress {
		progress := func(start int64) {
			fmt.Printf(" - uploaded %d of %d bytes (%.2f%%)\n", start, upl.ContentLength, float64(start)/float64(upl.ContentLength)*100)
		}
		fmt.Println("Unfinished upload found... resuming:")
		video, err := upl.Upload(ctx, progress)
		if err != nil {
			return fmt.Errorf("unable to upload: %w", err)
		}
		fmt.Println("Upload finished", video.Id)
	}

	return nil
}

func (s *Service) getUploader() (*resumer.Uploader, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home dir: %w", err)
	}
	stateFilePath := home + "/.config/wildernessprime/uploader-state.json"

	upl, err := resumer.NewGoogleDriveUploader(
		s.DriveService,
		s.YoutubeAccessToken,
		CHUNK_SIZE,
		stateFilePath,
	)
	if err != nil {
		return nil, fmt.Errorf("initialising uploader: %w", err)
	}
	return upl, nil

}
