package upload

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox"
	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox/files"
	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox/sharing"
	"golang.org/x/oauth2"
)

func (s *Service) InitDropboxService(ctx context.Context) error {

	if s.StorageService != DropboxStorage {
		return nil
	}

	cfg, err := GetDropboxConfig(ctx)
	if err != nil {
		return fmt.Errorf("loading Dropbox Config: %w", err)
	}

	s.DropboxConfig = cfg

	return nil
}

func GetDropboxConfig(ctx context.Context) (*dropbox.Config, error) {
	tokens, err := readDropboxTokens()
	if err != nil {
		return nil, fmt.Errorf("reading dropbox token: %w", err)
	}

	if tokens[DropboxClientID] == "" || tokens[DropboxClientSecret] == "" {
		return nil, fmt.Errorf("missing dropbox client id or secret")
	}

	conf := &oauth2.Config{
		ClientID:     tokens[DropboxClientID],
		ClientSecret: tokens[DropboxClientSecret],
		Endpoint:     dropbox.OAuthEndpoint(""),
	}

	var token *oauth2.Token

	if tokens[DropboxRefreshToken] == "" {

		fmt.Printf("1. Go to %v\n", conf.AuthCodeURL("state", oauth2.SetAuthURLParam("token_access_type", "offline")))
		fmt.Printf("2. Click \"Allow\" (you might have to log in first).\n")
		fmt.Printf("3. Copy the authorization code.\n")
		fmt.Printf("Enter the authorization code here: ")

		var code string
		if _, err = fmt.Scan(&code); err != nil {
			return nil, fmt.Errorf("scanning authorization code: %w", err)
		}
		ctx := context.Background()
		token, err = conf.Exchange(ctx, code)
		if err != nil {
			return nil, fmt.Errorf("exchanging authorization code: %w", err)
		}
		if token.RefreshToken == "" {
			return nil, fmt.Errorf("dropbox oauth2 response missing refresh token")
		}
		tokens[DropboxRefreshToken] = token.RefreshToken
		if err := writeDropboxTokens(tokens); err != nil {
			return nil, fmt.Errorf("writing dropbox tokens: %w", err)
		}
	} else {
		token = &oauth2.Token{
			RefreshToken: tokens[DropboxRefreshToken],
		}
	}

	client := oauth2.NewClient(ctx, conf.TokenSource(ctx, token))

	cfg := &dropbox.Config{
		Token:    token.AccessToken,
		LogLevel: dropbox.LogOff,
		Client:   client,
	}
	return cfg, nil
}

type DropboxKeys int

const (
	DropboxClientID     DropboxKeys = 1
	DropboxClientSecret DropboxKeys = 2
	DropboxRefreshToken DropboxKeys = 3
)

var DropboxKeyTypes = []DropboxKeys{
	DropboxClientID,
	DropboxClientSecret,
	DropboxRefreshToken,
}

func tokenFilepath(key DropboxKeys) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	filePath := path.Join(home, ".config", "wildernessprime")
	switch key {
	case DropboxClientID:
		filePath = path.Join(filePath, "dropbox-oauth-client-id.txt")
	case DropboxClientSecret:
		filePath = path.Join(filePath, "dropbox-oauth-client-secret.txt")
	case DropboxRefreshToken:
		filePath = path.Join(filePath, "dropbox-oauth-refresh-token.txt")
	default:
		return "", fmt.Errorf("unknown dropbox key type: %d", key)
	}
	return filePath, nil
}

func readDropboxTokens() (map[DropboxKeys]string, error) {
	tokens := map[DropboxKeys]string{}
	for _, key := range DropboxKeyTypes {
		filePath, err := tokenFilepath(key)
		if err != nil {
			return nil, fmt.Errorf("getting token filepath: %w", err)
		}
		fileBytes, err := os.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue // file doesn't exist; ignore
			}
			return nil, fmt.Errorf("reading token file: %w", err)
		}
		tokens[key] = strings.TrimSpace(string(fileBytes))
	}
	return tokens, nil
}

func writeDropboxTokens(tokens map[DropboxKeys]string) error {
	for key, token := range tokens {
		filePath, err := tokenFilepath(key)
		if err != nil {
			return fmt.Errorf("getting token filepath: %w", err)
		}
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			// Doesn't exist; lets create it
			err = os.MkdirAll(filepath.Dir(filePath), 0700)
			if err != nil {
				return fmt.Errorf("creating directory: %w", err)
			}
		}
		if err := os.WriteFile(filePath, []byte(token), 0600); err != nil {
			return fmt.Errorf("writing file: %w", err)
		}
	}
	return nil
}

func (s *Service) ClearDropboxPreviewFolder() error {

	if s.StorageService != DropboxStorage {
		return nil
	}

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

	if s.StorageService != DropboxStorage {
		return nil
	}

	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}

		var gotFiles bool
		var videoFiles, thumbnailFiles map[string]*files.FileMetadata

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
				videoFiles, err = getFilesInDropboxFolder(s.DropboxConfig, expedition.VideosDropbox)
				if err != nil {
					return fmt.Errorf("get video files (%v): %w", item.String(), err)
				}
				thumbnailFiles, err = getFilesInDropboxFolder(s.DropboxConfig, expedition.ThumbnailsDropbox)
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
						item.VideoDropbox = videoFiles[filename]
						break
					}
				}
				if item.VideoDropbox == nil {
					return fmt.Errorf("no video file found for regex %q (%v)", videoFilenameRegexBuffer.String(), item.String())
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
						item.ThumbnailDropbox = thumbnailFiles[filename]
						break
					}
				}
				if item.ThumbnailDropbox == nil {
					return fmt.Errorf("no thumbnail file found for regex %q (%v)", thumbnailFilenameRegexBuffer.String(), item.String())
				}
			}
		}
	}

	return nil
}

func getFilesInDropboxFolder(config *dropbox.Config, folderUrl string) (map[string]*files.FileMetadata, error) {

	dbx := files.New(*config)

	folderPath, err := getDropboxPathFromSharedLink(config, folderUrl)
	if err != nil {
		return nil, fmt.Errorf("extract dropbox path: %w", err)
	}

	// check if given object exists
	metaRes, err := getDropboxFileMetadata(dbx, folderPath)
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
				metaRes, err = getDropboxFileMetadata(dbx, folderPath)
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

func getDropboxFileMetadata(c files.Client, path string) (files.IsMetadata, error) {
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

	allFiles, err := getFilesInDropboxFolder(config, folderUrl)
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
