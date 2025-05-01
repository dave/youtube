package upload

import (
	"bytes"
	"fmt"
	"regexp"

	"google.golang.org/api/drive/v3"
)

func (s *Service) InitGoogleDriveService() error {

	if s.StorageService != GoogleDriveStorage {
		return nil
	}

	driveService, err := drive.New(s.ServiceAccountClient)
	if err != nil {
		return fmt.Errorf("unable to initialise drive service: %w", err)
	}
	s.DriveService = driveService

	return nil
}

func (s *Service) ClearGoogleDrivePreviewFolder() error {

	if s.StorageService != GoogleDriveStorage {
		return nil
	}

	if !s.Global.Preview {
		return nil
	}

	fmt.Println("Clearing preview folder")

	if err := deleteAllFilesInGoogleDriveFolder(s.DriveService, s.Global.PreviewThumbnailsFolder); err != nil {
		return fmt.Errorf("clearing preview folder: %w", err)
	}
	return nil
}

func (s *Service) FindGoogleDriveFiles() error {

	if s.StorageService != GoogleDriveStorage {
		return nil
	}

	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}

		var gotFiles bool
		var videoFiles, thumbnailFiles map[string]*drive.File

		for _, item := range expedition.Items {
			if !item.Video {
				continue // ignore all items which don't have a video
			}
			needVideo := item.YoutubeVideo == nil && s.Global.Production && item.Ready
			needThumbnail := s.Global.Thumbnails && expedition.HasThumbnails()

			if !needVideo && !needThumbnail {
				continue
			}

			if !gotFiles {
				var err error
				videoFiles, err = getFilesInGoogleDriveFolder(s.DriveService, expedition.VideosFolder)
				if err != nil {
					return fmt.Errorf("get video files (%v): %w", item.String(), err)
				}
				thumbnailFiles, err = getFilesInGoogleDriveFolder(s.DriveService, expedition.ThumbnailsFolder)
				if err != nil {
					return fmt.Errorf("get video files (%v): %w", item.String(), err)
				}
				gotFiles = true
			}

			if needVideo {
				videoFilenameRegexBuffer := bytes.NewBufferString("")
				if err := item.Expedition.Templates.ExecuteTemplate(videoFilenameRegexBuffer, "video_filename", item); err != nil {
					return fmt.Errorf("execute video filename regex template (%v): %w", item.String(), err)
				}
				videoFilenameRegex, err := regexp.Compile(videoFilenameRegexBuffer.String())
				if err != nil {
					return fmt.Errorf("compile video filename regex (%v): %w", item.String(), err)
				}
				for filename := range videoFiles {
					if videoFilenameRegex.MatchString(filename) {
						item.VideoGoogleDrive = videoFiles[filename]
						break
					}
				}
				if item.VideoGoogleDrive == nil {
					return fmt.Errorf("no video file found (%v)", item.String())
				}
			}

			if needThumbnail {
				thumbnailFilenameRegexBuffer := bytes.NewBufferString("")
				if err := item.Expedition.Templates.ExecuteTemplate(thumbnailFilenameRegexBuffer, "thumbnail_filename", item); err != nil {
					return fmt.Errorf("execute thumbnail filename regex template (%v): %w", item.String(), err)
				}
				thumbnailFilenameRegex, err := regexp.Compile(thumbnailFilenameRegexBuffer.String())
				if err != nil {
					return fmt.Errorf("compile thumbnail filename regex (%v): %w", item.String(), err)
				}
				for filename := range thumbnailFiles {
					if thumbnailFilenameRegex.MatchString(filename) {
						item.ThumbnailGoogleDrive = thumbnailFiles[filename]
						break
					}
				}
				if item.ThumbnailGoogleDrive == nil {
					return fmt.Errorf("no thumbnail file found (%v)", item.String())
				}
			}
		}
	}

	return nil
}

func getFilesInGoogleDriveFolder(srv *drive.Service, folderId string) (map[string]*drive.File, error) {
	var done bool
	var page string
	files := map[string]*drive.File{}

	for !done {
		query := fmt.Sprintf("'%s' in parents", folderId)
		response, err := srv.Files.List().Q(query).PageSize(50).Fields("nextPageToken, files(id, name)").PageToken(page).Do()
		if err != nil {
			return nil, fmt.Errorf("list files from drive: %w", err)
		}
		for _, file := range response.Files {
			files[file.Name] = file
		}
		page = response.NextPageToken
		if page == "" {
			done = true
		}
	}
	return files, nil
}

func deleteAllFilesInGoogleDriveFolder(srv *drive.Service, folderId string) error {
	// Step 1: List all files in the folder
	query := fmt.Sprintf("'%s' in parents and trashed = false", folderId)
	fileList, err := srv.Files.List().Q(query).Fields("files(id)").Do()
	if err != nil {
		return fmt.Errorf("listing files: %w", err)
	}

	// Step 2: Delete each file
	for _, file := range fileList.Files {
		fmt.Println("Deleting file from google drive:", file.Name)
		if err := srv.Files.Delete(file.Id).Do(); err != nil {
			return fmt.Errorf("deleting file %s: %w", file.Id, err)
		}
	}

	return nil
}
