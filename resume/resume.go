package resume

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox"
	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox/files"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/youtube/v3"
)

type Service struct {
	State         uploaderState
	Location      fileLocation
	AccessToken   string
	UploadURL     string
	ChunkSize     int64
	StateFile     string
	DriveService  *drive.Service
	DropboxConfig *dropbox.Config
	ContentFile   string
	ContentLength int64
}

func NewLocalFile(youtubeAccessToken string, chunkSize int64, stateFilePath string) (*Service, error) {

	u := &Service{
		Location:    LocationLocal,
		AccessToken: youtubeAccessToken,
		ChunkSize:   chunkSize,
		StateFile:   stateFilePath,
	}

	state, err := u.loadState()
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}
	u.State = state

	return u, nil
}

func NewGoogleDrive(driveService *drive.Service, youtubeAccessToken string, chunkSize int64, stateFilePath string) (*Service, error) {

	u := &Service{
		Location:     LocationGoogleDrive,
		DriveService: driveService,
		AccessToken:  youtubeAccessToken,
		ChunkSize:    chunkSize,
		StateFile:    stateFilePath,
	}

	state, err := u.loadState()
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}
	u.State = state

	return u, nil
}

func NewDropbox(dropboxConfig *dropbox.Config, youtubeAccessToken string, chunkSize int64, stateFilePath string) (*Service, error) {

	u := &Service{
		Location:      LocationDropbox,
		DropboxConfig: dropboxConfig,
		AccessToken:   youtubeAccessToken,
		ChunkSize:     chunkSize,
		StateFile:     stateFilePath,
	}

	state, err := u.loadState()
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}
	u.State = state

	return u, nil
}

func (s *Service) Initialise(contentFile string, data *youtube.Video) error {
	if s.State == StateUploadInProgress {
		return fmt.Errorf("upload already in progress")
	}
	s.ContentFile = contentFile
	switch s.Location {
	case LocationLocal:
		file, err := os.Open(contentFile)
		if err != nil {
			return fmt.Errorf("opening file: %w", err)
		}
		defer file.Close()
		fileInfo, err := file.Stat()
		if err != nil {
			return fmt.Errorf("getting file info: %w", err)
		}
		s.ContentLength = fileInfo.Size()
	case LocationGoogleDrive:
		sizeResponse, err := s.DriveService.Files.Get(s.ContentFile).Fields("size").Do()
		if err != nil {
			return fmt.Errorf("getting size of content: %w", err)
		}
		s.ContentLength = sizeResponse.Size
	case LocationDropbox:
		dbx := files.New(*s.DropboxConfig)
		metaRes, err := getDropboxFileMetadata(dbx, s.ContentFile)
		if err != nil {
			return fmt.Errorf("getting dropbox metadata: %w", err)
		}
		fileMeta, ok := metaRes.(*files.FileMetadata)
		if !ok {
			return fmt.Errorf("failed to get dropbox file metadata (%v)", s.ContentFile)
		}
		s.ContentLength = int64(fileMeta.Size)
	}

	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling meta data: %w", err)
	}

	req, err := http.NewRequest("POST", "https://www.googleapis.com/upload/youtube/v3/videos?uploadType=resumable&part=snippet,status", bytes.NewReader(dataBytes))
	if err != nil {
		return fmt.Errorf("creating new http request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("X-Upload-Content-Length", fmt.Sprintf("%d", s.ContentLength))
	req.Header.Set("X-Upload-Content-Type", "video/*")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("posting to upload api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to initiate upload, status %d: %v", resp.StatusCode, resp.Status)
	}

	s.UploadURL = resp.Header.Get("Location")

	if err := s.saveState(); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	return nil
}

type State struct {
	UploadUrl     string `json:"upload_url"`
	ContentFile   string `json:"content_file"`
	ContentLength int64  `json:"content_length"`
}

func (s *Service) saveState() error {
	state := State{
		UploadUrl:     s.UploadURL,
		ContentFile:   s.ContentFile,
		ContentLength: s.ContentLength,
	}
	stateMarshalled, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshalling state: %w", err)
	}
	if err := os.WriteFile(s.StateFile, stateMarshalled, 0644); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}
	return nil
}

func (s *Service) loadState() (uploaderState, error) {
	data, err := os.ReadFile(s.StateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StateIdle, nil
		}
		return 0, fmt.Errorf("reading state: %w", err)
	}
	state := &State{}
	if err := json.Unmarshal(data, state); err != nil {
		return 0, fmt.Errorf("unmarshalling state: %w", err)
	}
	s.UploadURL = state.UploadUrl
	s.ContentFile = state.ContentFile
	s.ContentLength = state.ContentLength
	return StateUploadInProgress, nil
}

func (s *Service) resume() (finished bool, video *youtube.Video, next int64, err error) {
	req, err := http.NewRequest("PUT", s.UploadURL, nil)
	if err != nil {
		return false, nil, 0, fmt.Errorf("creating new upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	req.Header.Set("Content-Length", "0")
	req.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", s.ContentLength))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return false, nil, 0, fmt.Errorf("sending upload request: %w", err)
	}
	defer resp.Body.Close()

	switch getStatus(resp.StatusCode) {
	case StatusDone:
		// file uploaded successfully, remove state file
		if err := os.Remove(s.StateFile); err != nil {
			return true, nil, 0, fmt.Errorf("removing state file: %w", err)
		}
		// read response body for video information
		v := &youtube.Video{}
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			return true, nil, 0, fmt.Errorf("decoding video information: %w", err)
		}
		return true, v, 0, nil
	case StatusResume:
		rangeHeader := resp.Header.Get("Range")
		if rangeHeader == "" {
			return false, nil, 0, nil
		}
		parts := strings.Split(rangeHeader, "-")
		if len(parts) != 2 {
			return false, nil, 0, fmt.Errorf("invalid range header format: %v", rangeHeader)
		}
		uploadedBytes, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return false, nil, 0, fmt.Errorf("invalid range header value: %w", err)
		}
		return false, nil, uploadedBytes + 1, nil
	default: // StatusFailed
		// upload permanently failed, remove state file (and ignore error)
		_ = os.Remove(s.StateFile)
		// read response body for error message
		errorMessage, _ := io.ReadAll(resp.Body)
		return false, nil, 0, fmt.Errorf("resume failed, status %d: %s", resp.StatusCode, errorMessage)
	}
}

func (s *Service) Upload(ctx context.Context, progress func(int64)) (*youtube.Video, error) {
	switch s.Location {
	case LocationLocal:
		video, err := s.uploadFile(progress)
		if err != nil {
			return nil, fmt.Errorf("uploading local file: %w", err)
		}
		return video, nil
	default:
		video, err := s.uploadFromCloud(ctx, progress)
		if err != nil {
			return nil, fmt.Errorf("uploading cloud file: %w", err)
		}
		return video, nil
	}
}

func (s *Service) uploadFile(progress func(int64)) (*youtube.Video, error) {
	file, err := os.Open(s.ContentFile)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	defer file.Close()

	done, video, start, err := s.resume()
	if err != nil {
		return nil, fmt.Errorf("getting uploaded bytes: %w", err)
	}

	if done {
		return video, nil
	}

	for start < s.ContentLength {
		progress(start)

		end := start + s.ChunkSize - 1
		if end >= s.ContentLength {
			end = s.ContentLength - 1
		}

		chunk := make([]byte, end-start+1)
		_, err := file.ReadAt(chunk, start)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("reading chunk: %w", err)
		}

		req, err := http.NewRequest("PUT", s.UploadURL, bytes.NewReader(chunk))
		if err != nil {
			return nil, fmt.Errorf("creating new chunk upload request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+s.AccessToken)
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, s.ContentLength))

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("sending chunk upload request: %w", err)
		}
		defer resp.Body.Close()

		switch getStatus(resp.StatusCode) {
		case StatusDone:
			// remove file
			if err := os.Remove(s.StateFile); err != nil {
				return nil, fmt.Errorf("removing state file: %w", err)
			}
			// read response body for video information
			v := &youtube.Video{}
			if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
				return nil, fmt.Errorf("decoding video information: %w", err)
			}
			return v, nil
		case StatusResume:
			start = end + 1
			continue // Skip to the next chunk
		default: // StatusFailed
			// upload permanently failed, remove state file (and ignore error)
			_ = os.Remove(s.StateFile)
			// read response body for error message
			errorMessage, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("uploading chunk failed, status %d: %s", resp.StatusCode, errorMessage)
		}
	}
	return nil, fmt.Errorf("finished without getting a success response")
}

func (s *Service) uploadFromCloud(ctx context.Context, progress func(int64)) (*youtube.Video, error) {
	done, video, start, err := s.resume()
	if err != nil {
		return nil, fmt.Errorf("getting uploaded bytes: %w", err)
	}
	if done {
		return video, nil
	}

	var download io.ReadCloser
	switch s.Location {
	case LocationGoogleDrive:
		downloadRequest := s.DriveService.Files.Get(s.ContentFile).Context(ctx)
		downloadRequest.Header().Set("Range", fmt.Sprintf("bytes=%d-", start))
		downloadResponse, err := downloadRequest.Download()
		if err != nil {
			return nil, fmt.Errorf("starting Google Drive download: %w", err)
		}
		download = downloadResponse.Body
	case LocationDropbox:
		dbx := files.New(*s.DropboxConfig)
		arg := files.NewDownloadArg(s.ContentFile)
		arg.ExtraHeaders = map[string]string{
			"Range": fmt.Sprintf("bytes=%d-", start),
		}
		_, download, err = dbx.Download(arg)
		if err != nil {
			return nil, fmt.Errorf("downloading dropbox file: %w", err)
		}
	}
	defer download.Close()

	client := &http.Client{}

	for {
		progress(start)

		var last bool
		bytesToRead := s.ChunkSize
		if start+s.ChunkSize > s.ContentLength {
			last = true
			bytesToRead = s.ContentLength - start
		}

		uploadReq, err := http.NewRequest("PUT", s.UploadURL, NewChunkReader(download, s.ChunkSize))
		if err != nil {
			return nil, fmt.Errorf("creating upload request: %w", err)
		}

		end := start + bytesToRead - 1
		uploadReq.Header.Set("Authorization", "Bearer "+s.AccessToken)
		uploadReq.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, s.ContentLength))

		resp, err := client.Do(uploadReq)
		if err != nil {
			return nil, fmt.Errorf("sending chunk upload request: %w", err)
		}

		switch getStatus(resp.StatusCode) {
		case StatusDone:
			// upload complete - remove state
			if err := os.Remove(s.StateFile); err != nil {
				_ = resp.Body.Close()
				return nil, fmt.Errorf("removing state file: %w", err)
			}
			// read response body for video information
			v := &youtube.Video{}
			if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
				_ = resp.Body.Close()
				return nil, fmt.Errorf("decoding video information: %w", err)
			}
			_ = resp.Body.Close()
			return v, nil
		case StatusResume:
			if last {
				// upload complete - remove state
				if err := os.Remove(s.StateFile); err != nil {
					_ = resp.Body.Close()
					return nil, fmt.Errorf("removing state file: %w", err)
				}
				// read response body for video information
				v := &youtube.Video{}
				if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
					_ = resp.Body.Close()
					return nil, fmt.Errorf("decoding video information: %w", err)
				}
				_ = resp.Body.Close()
				return v, nil
			}
			start = end + 1
			_ = resp.Body.Close()
			continue
		case StatusFailed:
			// upload permanently failed, remove state file (and ignore error)
			_ = os.Remove(s.StateFile)
			// read response body for error message
			errorMessage, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return nil, fmt.Errorf("uploading chunk failed, status %d: %s", resp.StatusCode, errorMessage)
		}
		_ = resp.Body.Close()
	}
}

type ChunkReader struct {
	Reader    io.Reader
	ChunkSize int64
	readCount int64
}

func NewChunkReader(r io.Reader, chunkSize int64) *ChunkReader {
	return &ChunkReader{
		Reader:    r,
		ChunkSize: chunkSize,
		readCount: 0,
	}
}

func (cr *ChunkReader) Read(p []byte) (n int, err error) {
	if cr.readCount >= cr.ChunkSize {
		return 0, io.EOF
	}

	// Limit the read to the chunk size minus what has already been read
	remaining := cr.ChunkSize - cr.readCount
	if remaining < int64(len(p)) {
		p = p[:remaining]
	}

	// Read from the underlying reader
	n, err = cr.Reader.Read(p)
	cr.readCount += int64(n)

	// If we reach the chunk limit, we return EOF
	if cr.readCount >= cr.ChunkSize {
		return n, io.EOF
	}

	return n, err
}

type fileLocation int

const (
	LocationDropbox     fileLocation = 0
	LocationGoogleDrive fileLocation = 1
	LocationLocal       fileLocation = 2
)

type uploaderState int

const (
	StateUploadInProgress uploaderState = 1
	StateIdle             uploaderState = 2
)

type responseStatus int

const (
	StatusDone   responseStatus = 1
	StatusResume responseStatus = 2
	StatusFailed responseStatus = 3
)

func getStatus(code int) responseStatus {
	switch code {
	case http.StatusCreated:
		// Response is done
		return StatusDone
	case http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		308, /* Resume Incomplete */
		http.StatusOK:
		// Response can be resumed
		return StatusResume
	default:
		// Response is failed
		return StatusFailed
	}
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
