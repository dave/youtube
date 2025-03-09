package main

import (
	"bytes"
	"fmt"
	"regexp"

	"google.golang.org/api/drive/v3"
)

func (s *Service) InitDriveService() error {

	driveService, err := drive.New(s.ServiceAccountClient)
	if err != nil {
		return fmt.Errorf("unable to initialise drive service: %w", err)
	}
	s.DriveService = driveService

	return nil
}

func (s *Service) FindDriveFiles() error {

	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		if !expedition.Ready {
			continue
		}

		var gotFiles bool
		var videoFiles, thumbnailFiles map[string]*drive.File

		for _, item := range expedition.Items {
			if !item.Video {
				continue // ignore all items which don't have a video
			}
			if !item.Ready {
				continue // ignore all items which aren't ready
			}
			needVideo := item.YoutubeVideo == nil
			needThumbnail := expedition.Thumbnails

			if !needVideo && !needThumbnail {
				continue
			}

			if !gotFiles {
				var err error
				videoFiles, err = getFilesInFolder(s.DriveService, expedition.VideosFolder)
				if err != nil {
					return fmt.Errorf("get video files: %w", err)
				}
				thumbnailFiles, err = getFilesInFolder(s.DriveService, expedition.ThumbnailsFolder)
				if err != nil {
					return fmt.Errorf("get video files: %w", err)
				}
				gotFiles = true
			}

			if needVideo {
				videoFilenameRegexBuffer := bytes.NewBufferString("")
				if err := item.Expedition.Templates.ExecuteTemplate(videoFilenameRegexBuffer, "video_filename", item); err != nil {
					return fmt.Errorf("execute video filename regex template: %w", err)
				}
				videoFilenameRegex, err := regexp.Compile(videoFilenameRegexBuffer.String())
				if err != nil {
					return fmt.Errorf("compile video filename regex: %w", err)
				}
				for filename := range videoFiles {
					if videoFilenameRegex.MatchString(filename) {
						item.VideoFile = videoFiles[filename]
						break
					}
				}
				if item.VideoFile == nil {
					return fmt.Errorf("no video file found for item %s", item)
				}
			}

			if needThumbnail {
				thumbnailFilenameRegexBuffer := bytes.NewBufferString("")
				if err := item.Expedition.Templates.ExecuteTemplate(thumbnailFilenameRegexBuffer, "thumbnail_filename", item); err != nil {
					return fmt.Errorf("execute thumbnail filename regex template: %w", err)
				}
				thumbnailFilenameRegex, err := regexp.Compile(thumbnailFilenameRegexBuffer.String())
				if err != nil {
					return fmt.Errorf("compile thumbnail filename regex: %w", err)
				}
				for filename := range thumbnailFiles {
					if thumbnailFilenameRegex.MatchString(filename) {
						item.ThumbnailFile = thumbnailFiles[filename]
						fmt.Println("Found thumbnail file", item.String(), item.ThumbnailFile.Name)
						break
					}
				}
				if item.ThumbnailFile == nil {
					return fmt.Errorf("no thumbnail file found for item %s", item)
				}
			}
		}
	}

	return nil
}

func getFilesInFolder(srv *drive.Service, folderId string) (map[string]*drive.File, error) {
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
