package upload

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox"
	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox/files"
	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox/sharing"
)

func (s *Service) InitDropboxService() error {
	token, err := readDropboxToken()
	if err != nil {
		return fmt.Errorf("reading dropbox token: %w", err)
	}
	s.DropboxConfig = &dropbox.Config{
		Token:           token,
		LogLevel:        dropbox.LogOff, // dropbox.LogInfo,
		Logger:          nil,
		AsMemberID:      "",
		Domain:          "",
		Client:          nil,
		HeaderGenerator: nil,
		URLGenerator:    nil,
	}
	return nil
}

func readDropboxToken() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	filePath := path.Join(home, ".config", "wildernessprime", "dropbox-oauth-access-token.txt")
	b, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func getDropboxPathFromSharedLink(config *dropbox.Config, sharedLink string) (string, error) {
	dbx := sharing.New(*config)
	arg := sharing.NewGetSharedLinkMetadataArg(sharedLink)

	res, err := dbx.GetSharedLinkMetadata(arg)
	if err != nil {
		return "", fmt.Errorf("get shared link metadata: %w", err)
	}

	switch meta := res.(type) {
	case *sharing.FileLinkMetadata:
		return meta.PathLower, nil
	case *sharing.FolderLinkMetadata:
		return meta.PathLower, nil
	default:
		return "", fmt.Errorf("unsupported shared link type")
	}
}

func (s *Service) ClearDropboxPreviewFolder() error {
	if !s.Global.Preview {
		return nil
	}

	fmt.Println("Clearing preview folder")

	if err := removeDropboxFiles(s.DropboxConfig, s.Global.PreviewThumbnailsDropbox); err != nil {
		return fmt.Errorf("clearing preview folder: %w", err)
	}
	return nil
}

func (s *Service) FindDropboxFiles() error {

	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		if !expedition.Ready {
			continue
		}

		var gotFiles bool
		var videoFiles, thumbnailFiles map[string]*files.FileMetadata

		for _, item := range expedition.Items {
			if !item.Video {
				continue // ignore all items which don't have a video
			}
			if !item.Ready {
				continue // ignore all items which aren't ready
			}
			needVideo := item.YoutubeVideo == nil && s.Global.Production
			needThumbnail := expedition.Thumbnails

			if !needVideo && !needThumbnail {
				continue
			}

			if !gotFiles {
				var err error
				videoFiles, err = getDropboxFilesInFolder(s.DropboxConfig, expedition.VideosDropbox)
				if err != nil {
					return fmt.Errorf("get video files (%v): %w", item.String(), err)
				}
				thumbnailFiles, err = getDropboxFilesInFolder(s.DropboxConfig, expedition.ThumbnailsDropbox)
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
						item.VideoDropboxFile = videoFiles[filename]
						break
					}
				}
				if item.VideoDropboxFile == nil {
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
						item.ThumbnailDropboxFile = thumbnailFiles[filename]
						break
					}
				}
				if item.ThumbnailDropboxFile == nil {
					return fmt.Errorf("no thumbnail file found (%v)", item.String())
				}
			}
		}
	}

	return nil
}

func getDropboxFilesInFolder(config *dropbox.Config, folderUrl string) (map[string]*files.FileMetadata, error) {

	dbx := files.New(*config)

	folderPath, err := getDropboxPathFromSharedLink(config, folderUrl)
	if err != nil {
		return nil, fmt.Errorf("extract dropbox path: %w", err)
	}

	// check if given object exists
	metaRes, err := getFileMetadata(dbx, folderPath)
	if err != nil {
		return nil, fmt.Errorf("get dropbox metadata: %w", err)
	}

	if _, ok := metaRes.(*files.FolderMetadata); !ok {
		return nil, fmt.Errorf("path is not a folder: %s", folderPath)
	}

	arg := files.NewListFolderArg(folderPath)
	arg.Recursive = false
	arg.IncludeDeleted = false

	var entries []files.IsMetadata

	res, err := dbx.ListFolder(arg)
	if err != nil {
		listRevisionError, ok := err.(files.ListRevisionsAPIError)
		if ok {
			// Don't treat a "not_folder" error as fatal; recover by sending a
			// get_metadata request for the same path and using that response instead.
			if listRevisionError.EndpointError.Path.Tag == files.LookupErrorNotFolder {
				var metaRes files.IsMetadata
				metaRes, err = getFileMetadata(dbx, folderPath)
				entries = []files.IsMetadata{metaRes}
			} else {
				// Return if there's an error other than "not_folder" or if the follow-up
				// metadata request fails.
				return nil, fmt.Errorf("list dropbox folder: %w", err)
			}
		} else {
			return nil, fmt.Errorf("list dropbox folder: %w", err)
		}
	} else {
		entries = res.Entries

		for res.HasMore {
			arg := files.NewListFolderContinueArg(res.Cursor)

			res, err = dbx.ListFolderContinue(arg)
			if err != nil {
				return nil, fmt.Errorf("list dropbox folder has more: %w", err)
			}

			entries = append(entries, res.Entries...)
		}
	}

	filesMap := map[string]*files.FileMetadata{}
	for _, entry := range entries {
		switch f := entry.(type) {
		case *files.FileMetadata:
			filesMap[f.Name] = f
		case *files.FolderMetadata:
			// ignore
		case *files.DeletedMetadata:
			// ignore
		}
	}

	return filesMap, nil
}

// Sends a get_metadata request for a given path and returns the response
func getFileMetadata(c files.Client, path string) (files.IsMetadata, error) {
	arg := files.NewGetMetadataArg(path)

	arg.IncludeDeleted = true

	res, err := c.GetMetadata(arg)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func removeDropboxFiles(config *dropbox.Config, folderUrl string) error {

	var filesMetadata []*files.FileMetadata
	dbx := files.New(*config)

	allFiles, err := getDropboxFilesInFolder(config, folderUrl)
	for _, metadata := range allFiles {
		filesMetadata = append(filesMetadata, metadata)
	}

	// Execute removals
	for _, fileToDelete := range filesMetadata {
		fmt.Println("Deleting file from dropbox:", fileToDelete.PathDisplay)
		arg := files.NewDeleteArg(fileToDelete.Id)
		if _, err = dbx.DeleteV2(arg); err != nil {
			return fmt.Errorf("delete dropbox files: %w", err)
		}
	}

	return nil
}
