package uploader

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

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/youtube/v3"
)

type Uploader struct {
	State         uploaderState
	Location      fileLocation
	AccessToken   string
	UploadURL     string
	ChunkSize     int64
	StateFile     string
	DriveService  *drive.Service
	ContentFile   string
	ContentLength int64
}

func NewLocalFileUploader(youtubeAccessToken string, chunkSize int64, stateFilePath string) (*Uploader, error) {

	u := &Uploader{
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

func NewGoogleDriveUploader(driveService *drive.Service, youtubeAccessToken string, chunkSize int64, stateFilePath string) (*Uploader, error) {

	u := &Uploader{
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

func (u *Uploader) Initialise(contentFile string, data *youtube.Video) error {
	if u.State == StateUploadInProgress {
		return fmt.Errorf("upload already in progress")
	}
	u.ContentFile = contentFile
	switch u.Location {
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
		u.ContentLength = fileInfo.Size()
	case LocationGoogleDrive:
		sizeResponse, err := u.DriveService.Files.Get(u.ContentFile).Fields("size").Do()
		if err != nil {
			return fmt.Errorf("getting size of content: %w", err)
		}
		u.ContentLength = sizeResponse.Size
	}

	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling meta data: %w", err)
	}

	req, err := http.NewRequest("POST", "https://www.googleapis.com/upload/youtube/v3/videos?uploadType=resumable&part=snippet,status", bytes.NewReader(dataBytes))
	if err != nil {
		return fmt.Errorf("creating new http request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+u.AccessToken)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("X-Upload-Content-Length", fmt.Sprintf("%d", u.ContentLength))
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

	u.UploadURL = resp.Header.Get("Location")

	if err := u.saveState(); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	return nil
}

type State struct {
	UploadUrl     string `json:"upload_url"`
	ContentFile   string `json:"content_file"`
	ContentLength int64  `json:"content_length"`
}

func (u *Uploader) saveState() error {
	state := State{
		UploadUrl:     u.UploadURL,
		ContentFile:   u.ContentFile,
		ContentLength: u.ContentLength,
	}
	stateMarshalled, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshalling state: %w", err)
	}
	if err := os.WriteFile(u.StateFile, stateMarshalled, 0644); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}
	return nil
}

func (u *Uploader) loadState() (uploaderState, error) {
	data, err := os.ReadFile(u.StateFile)
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
	u.UploadURL = state.UploadUrl
	u.ContentFile = state.ContentFile
	u.ContentLength = state.ContentLength
	return StateUploadInProgress, nil
}

func (u *Uploader) resume() (finished bool, video *youtube.Video, next int64, err error) {
	req, err := http.NewRequest("PUT", u.UploadURL, nil)
	if err != nil {
		return false, nil, 0, fmt.Errorf("creating new upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+u.AccessToken)
	req.Header.Set("Content-Length", "0")
	req.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", u.ContentLength))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return false, nil, 0, fmt.Errorf("sending upload request: %w", err)
	}
	defer resp.Body.Close()

	switch getStatus(resp.StatusCode) {
	case StatusDone:
		// file uploaded successfully, remove state file
		if err := os.Remove(u.StateFile); err != nil {
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
		_ = os.Remove(u.StateFile)
		// read response body for error message
		errorMessage, _ := io.ReadAll(resp.Body)
		return false, nil, 0, fmt.Errorf("resume failed, status %d: %s", resp.StatusCode, errorMessage)
	}
}

func (u *Uploader) Upload(ctx context.Context, progress func(int64)) (*youtube.Video, error) {
	switch u.Location {
	case LocationLocal:
		video, err := u.uploadFile(progress)
		if err != nil {
			return nil, fmt.Errorf("uploading local file: %w", err)
		}
		return video, nil
	default: // LocationGoogleDrive
		video, err := u.uploadFromDrive(ctx, progress)
		if err != nil {
			return nil, fmt.Errorf("uploading drive file: %w", err)
		}
		return video, nil
	}
}

func (u *Uploader) uploadFile(progress func(int64)) (*youtube.Video, error) {
	file, err := os.Open(u.ContentFile)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	defer file.Close()

	done, video, start, err := u.resume()
	if err != nil {
		return nil, fmt.Errorf("getting uploaded bytes: %w", err)
	}

	if done {
		return video, nil
	}

	for start < u.ContentLength {
		progress(start)

		end := start + u.ChunkSize - 1
		if end >= u.ContentLength {
			end = u.ContentLength - 1
		}

		chunk := make([]byte, end-start+1)
		_, err := file.ReadAt(chunk, start)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("reading chunk: %w", err)
		}

		req, err := http.NewRequest("PUT", u.UploadURL, bytes.NewReader(chunk))
		if err != nil {
			return nil, fmt.Errorf("creating new chunk upload request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+u.AccessToken)
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, u.ContentLength))

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("sending chunk upload request: %w", err)
		}
		defer resp.Body.Close()

		switch getStatus(resp.StatusCode) {
		case StatusDone:
			// remove file
			if err := os.Remove(u.StateFile); err != nil {
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
			_ = os.Remove(u.StateFile)
			// read response body for error message
			errorMessage, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("uploading chunk failed, status %d: %s", resp.StatusCode, errorMessage)
		}
	}
	return nil, fmt.Errorf("finished without getting a success response")
}

func (u *Uploader) uploadFromDrive(ctx context.Context, progress func(int64)) (*youtube.Video, error) {
	done, video, start, err := u.resume()
	if err != nil {
		return nil, fmt.Errorf("getting uploaded bytes: %w", err)
	}
	if done {
		return video, nil
	}
	downloadRequest := u.DriveService.Files.Get(u.ContentFile).Context(ctx)
	downloadRequest.Header().Set("Range", fmt.Sprintf("bytes=%d-", start))
	download, err := downloadRequest.Download()
	if err != nil {
		return nil, fmt.Errorf("starting Google Drive download: %w", err)
	}
	defer download.Body.Close()

	client := &http.Client{}

	for {
		progress(start)

		var last bool
		bytesToRead := u.ChunkSize
		if start+u.ChunkSize > u.ContentLength {
			last = true
			bytesToRead = u.ContentLength - start
		}

		uploadReq, err := http.NewRequest("PUT", u.UploadURL, NewChunkReader(download.Body, u.ChunkSize))
		if err != nil {
			return nil, fmt.Errorf("creating upload request: %w", err)
		}

		end := start + bytesToRead - 1
		uploadReq.Header.Set("Authorization", "Bearer "+u.AccessToken)
		uploadReq.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, u.ContentLength))

		resp, err := client.Do(uploadReq)
		if err != nil {
			return nil, fmt.Errorf("sending chunk upload request: %w", err)
		}
		defer resp.Body.Close()

		switch getStatus(resp.StatusCode) {
		case StatusDone:
			// upload complete - remove state
			if err := os.Remove(u.StateFile); err != nil {
				return nil, fmt.Errorf("removing state file: %w", err)
			}
			// read response body for video information
			v := &youtube.Video{}
			if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
				return nil, fmt.Errorf("decoding video information: %w", err)
			}
			return v, nil
		case StatusResume:
			if last {
				// upload complete - remove state
				if err := os.Remove(u.StateFile); err != nil {
					return nil, fmt.Errorf("removing state file: %w", err)
				}
				// read response body for video information
				v := &youtube.Video{}
				if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
					return nil, fmt.Errorf("decoding video information: %w", err)
				}
				return v, nil
			}
			start = end + 1
			continue
		case StatusFailed:
			// upload permanently failed, remove state file (and ignore error)
			_ = os.Remove(u.StateFile)
			// read response body for error message
			errorMessage, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("uploading chunk failed, status %d: %s", resp.StatusCode, errorMessage)
		}
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
