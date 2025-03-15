package main

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"sync"

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

func (s *Service) ClearPreviewFolder() error {
	if !s.Global.Preview {
		return nil
	}

	fmt.Println("Clearing preview folder")

	if err := deleteAllFilesInFolder(s.DriveService, s.Global.PreviewThumbnailsFolder); err != nil {
		return fmt.Errorf("clearing preview folder: %w", err)
	}
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

func deleteAllFilesInFolder(srv *drive.Service, folderId string) error {
	// Step 1: List all files in the folder
	query := fmt.Sprintf("'%s' in parents and trashed = false", folderId)
	fileList, err := srv.Files.List().Q(query).Fields("files(id)").Do()
	if err != nil {
		return fmt.Errorf("listing files: %w", err)
	}

	// Step 2: Delete each file
	for _, file := range fileList.Files {
		if err := srv.Files.Delete(file.Id).Do(); err != nil {
			return fmt.Errorf("deleting file %s: %w", file.Id, err)
		}
	}

	return nil
}

type DriveReaderAt struct {
	service  *drive.Service
	fileID   string
	mu       sync.Mutex
	resp     io.ReadCloser // Current open response body
	lastOff  int64         // Last read offset
	lastSize int           // Last read size
	closed   bool          // Track if the reader is closed
}

// NewDriveReaderAt initializes a DriveReaderAt.
func NewDriveReaderAt(svc *drive.Service, fileID string) *DriveReaderAt {
	return &DriveReaderAt{service: svc, fileID: fileID}
}

// ReadAt reads from Google Drive at a specific offset.
func (d *DriveReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return 0, fmt.Errorf("DriveReaderAt is closed")
	}

	// Check if we can reuse the existing response
	if d.resp != nil && off == d.lastOff+int64(d.lastSize) {
		n, err = io.ReadFull(d.resp, p)
		if err == io.EOF {
			d.resp.Close()
			d.resp = nil
		}
		d.lastOff = off
		d.lastSize = n
		return n, err
	}

	// Close previous response if it exists (non-contiguous read)
	if d.resp != nil {
		d.resp.Close()
		d.resp = nil
	}

	// Fetch the new byte range
	req := d.service.Files.Get(d.fileID)
	req.Header().Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+int64(len(p))-1))

	resp, err := req.Download()
	if err != nil {
		return 0, err
	}

	d.resp = resp.Body
	d.lastOff = off
	d.lastSize = len(p)

	n, err = io.ReadFull(d.resp, p)
	if err == io.EOF {
		_ = d.resp.Close()
		d.resp = nil
	}
	return n, err
}

// Close ensures the reader releases resources.
func (d *DriveReaderAt) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil
	}

	if d.resp != nil {
		_ = d.resp.Close()
		d.resp = nil
	}
	d.closed = true
	return nil
}
