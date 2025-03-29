package upload

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox"
	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox/auth"
	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox/files"
)

const singleShotUploadSizeCutoff int64 = 32 * (1 << 20)

type uploadChunk struct {
	data   []byte
	offset uint64
	close  bool
}

func uploadOneChunk(dbx files.Client, args *files.UploadSessionAppendArg, data []byte) error {
	for {
		err := dbx.UploadSessionAppendV2(args, bytes.NewReader(data))
		if err != nil {
			switch errt := err.(type) {
			case auth.RateLimitAPIError:
				time.Sleep(time.Second * time.Duration(errt.RateLimitError.RetryAfter))
				continue
			default:
				return err
			}
		}
		return nil
	}
}

func uploadChunked(dbx files.Client, r io.Reader, commitInfo *files.CommitInfo, sizeTotal int64, workers int, chunkSize int64) (err error) {
	startArgs := files.NewUploadSessionStartArg()
	startArgs.SessionType = &files.UploadSessionType{}
	startArgs.SessionType.Tag = files.UploadSessionTypeConcurrent
	res, err := dbx.UploadSessionStart(startArgs, nil)
	if err != nil {
		return fmt.Errorf("upload session start: %w", err)
	}

	wg := sync.WaitGroup{}
	workCh := make(chan uploadChunk, workers)
	errCh := make(chan error, 1)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for chunk := range workCh {
				cursor := files.NewUploadSessionCursor(res.SessionId, chunk.offset)
				args := files.NewUploadSessionAppendArg(cursor)
				args.Close = chunk.close

				if err := uploadOneChunk(dbx, args, chunk.data); err != nil {
					errCh <- err
				}
			}
		}()
	}

	written := int64(0)
	for written < sizeTotal {
		data, err := io.ReadAll(&io.LimitedReader{R: r, N: chunkSize})
		if err != nil {
			return fmt.Errorf("read chunk: %w", err)
		}
		expectedLen := chunkSize
		if written+chunkSize > sizeTotal {
			expectedLen = sizeTotal - written
		}
		if len(data) != int(expectedLen) {
			return fmt.Errorf("failed to read %d bytes from source", expectedLen)
		}

		chunk := uploadChunk{
			data:   data,
			offset: uint64(written),
			close:  written+chunkSize >= sizeTotal,
		}

		select {
		case workCh <- chunk:
		case err := <-errCh:
			return err
		}

		written += int64(len(data))
	}

	close(workCh)
	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
	}

	cursor := files.NewUploadSessionCursor(res.SessionId, uint64(written))
	args := files.NewUploadSessionFinishArg(cursor, commitInfo)
	_, err = dbx.UploadSessionFinish(args, nil)
	if err != nil {
		return fmt.Errorf("upload session finish: %w", err)
	}
	return nil
}

func uploadToDropbox(config *dropbox.Config, data io.Reader, path string, size int64) error {

	commitInfo := files.NewCommitInfo(path)
	commitInfo.Mode.Tag = "overwrite"

	// The Dropbox API only accepts timestamps in UTC with second precision.
	ts := time.Now().UTC().Round(time.Second)
	commitInfo.ClientModified = &ts

	dbx := files.New(*config)

	// TODO: re-enable if we need to upload large files. For now, we are only uploading thumbnails which will be small.
	//if size > singleShotUploadSizeCutoff {
	//	if err := uploadChunked(dbx, data, commitInfo, size, 1, 4*1024*1024); err != nil {
	//		return fmt.Errorf("chunked upload: %w", err)
	//	}
	//}

	uploadArg := files.NewUploadArg(path)
	uploadArg.CommitInfo = *commitInfo
	if _, err := dbx.Upload(uploadArg, data); err != nil {
		return fmt.Errorf("upload file: %w", err)
	}

	return nil
}
