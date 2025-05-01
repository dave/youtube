package upload

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"os"
	"path"

	"github.com/disintegration/imaging"
	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox/files"
	"github.com/edwvee/exiffix"
	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"google.golang.org/api/drive/v3"
)

func (s *Service) UpdateThumbnails() error {
	// find all the videos which need to be updated
	if !s.Global.Thumbnails {
		return nil
	}
	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		for _, item := range expedition.Items {
			if !item.Video {
				continue
			}
			if !item.DoThumbnail {
				continue
			}
			if item.YoutubeVideo == nil && !s.Global.Preview {
				// if we're not in preview mode, we can only update the thumbnail if the video has been uploaded
				continue
			}
			if err := updateThumbnail(s, item); err != nil {
				return fmt.Errorf("updating thumbnail (%v): %w", item.String(), err)
			}
		}
	}
	return nil
}

func updateThumbnail(s *Service, item *Item) error {

	textTopBuffer := bytes.NewBufferString("")
	if err := item.Expedition.Templates.ExecuteTemplate(textTopBuffer, "thumbnail_top", item); err != nil {
		return fmt.Errorf("execute thumbnail top template (%v): %w", item.String(), err)
	}
	textBottomBuffer := bytes.NewBufferString("")
	if err := item.Expedition.Templates.ExecuteTemplate(textBottomBuffer, "thumbnail_bottom", item); err != nil {
		return fmt.Errorf("execute thumbnail top template (%v): %w", item.String(), err)
	}

	fmt.Printf("Updating thumbnail (%v)\n", item.String())
	var download io.ReadCloser
	switch s.StorageService {
	case GoogleDriveStorage:
		response, err := s.DriveService.Files.Get(item.ThumbnailGoogleDrive.Id).Download()
		if err != nil {
			return fmt.Errorf("downloading drive file (%v): %w", item.String(), err)
		}
		download = response.Body
	case DropboxStorage:
		dbx := files.New(*s.DropboxConfig)
		arg := files.NewDownloadArg(item.ThumbnailDropbox.Id)
		var err error
		_, download, err = dbx.Download(arg)
		if err != nil {
			return fmt.Errorf("downloading dropbox file (%v): %w", item.String(), err)
		}
	}

	transformed, err := transformImage(download, textTopBuffer.String(), textBottomBuffer.String())
	if err != nil {
		_ = download.Close()
		return fmt.Errorf("transforming thumbnail (%v): %w", item.String(), err)
	}
	_ = download.Close()

	transformedBytes, err := io.ReadAll(transformed)
	if err != nil {
		return fmt.Errorf("reading thumbnail (%v): %w", item.String(), err)
	}

	if s.Global.Preview {
		s.StoreVideoPreview(item, "thumbnail_top", "", textTopBuffer.String())
		s.StoreVideoPreview(item, "thumbnail_bottom", "", textBottomBuffer.String())
		switch s.StorageService {
		case GoogleDriveStorage:
			fileMetadata := &drive.File{
				Name:    fmt.Sprintf("[%v]", item.String()),
				Parents: []string{s.Global.PreviewThumbnailsFolder},
			}
			if _, err := s.DriveService.Files.Create(fileMetadata).Media(bytes.NewReader(transformedBytes)).Do(); err != nil {
				return fmt.Errorf("creating preview thumbnail on google drive (%v): %w", item.String(), err)
			}
		case DropboxStorage:
			previewThumbnailsDropbox, err := getDropboxPathFromSharedLink(s.DropboxConfig, s.Global.PreviewThumbnailsDropbox)
			if err != nil {
				return fmt.Errorf("getting dropbox path (%v): %w", item.String(), err)
			}
			filePath := path.Join(previewThumbnailsDropbox, fmt.Sprintf("[%v].jpg", item.String()))
			if err := uploadToDropbox(s.DropboxConfig, bytes.NewReader(transformedBytes), filePath, int64(len(transformedBytes))); err != nil {
				return fmt.Errorf("creating preview thumbnail on dropbox (%v): %w", item.String(), err)
			}
		}
	}
	if s.Global.Production && item.Ready {
		if item.YoutubeVideo == nil {
			return fmt.Errorf("item has no youtube video (%v)", item.String())
		}
		if _, err := s.YoutubeService.Thumbnails.Set(item.YoutubeVideo.Id).Media(bytes.NewReader(transformedBytes)).Do(); err != nil {
			return fmt.Errorf("setting thumbnail (%v): %w", item.String(), err)
		}
	}
	return nil

}

func transformImage(file io.Reader, textTop, textBottom string) (io.Reader, error) {
	imgIn, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("reading image: %w", err)
	}
	imgBuffer := bytes.NewReader(imgIn)

	img, _, err := exiffix.Decode(imgBuffer)
	if err != nil {
		return nil, fmt.Errorf("decoding image: %w", err)
	}

	width := 1280
	height := 720
	rgba := imaging.Fill(img, width, height, imaging.Center, imaging.Lanczos)

	bold, err := getFont("JosefinSans-Bold.ttf")
	if err != nil {
		return nil, err
	}
	regular, err := getFont("JosefinSans-Regular.ttf")
	if err != nil {
		return nil, err
	}

	fg := image.White
	c := freetype.NewContext()
	c.SetDPI(72)
	c.SetFontSize(75)
	c.SetClip(rgba.Bounds())
	c.SetDst(rgba)
	c.SetSrc(fg)
	c.SetHinting(font.HintingNone) // font.HintingFull

	// calculate the size of the text by drawing it onto a blank image
	c.SetDst(image.NewRGBA(image.Rect(0, 0, width, height)))
	c.SetFont(bold)
	posTop, err := c.DrawString(textTop, freetype.Pt(0, 0))
	if err != nil {
		return nil, fmt.Errorf("measuring textTop: %w", err)
	}

	c.SetDst(image.NewRGBA(image.Rect(0, 0, width, height)))
	c.SetFont(regular)
	posBottom, err := c.DrawString(textBottom, freetype.Pt(0, 0))
	if err != nil {
		return nil, fmt.Errorf("measuring textBottom: %w", err)
	}
	c.SetDst(rgba)

	if textTop != "" {
		// Draw background
		draw.Draw(
			rgba,
			image.Rectangle{
				Min: image.Point{
					X: rgba.Bounds().Max.X - posTop.X.Round() - 100,
					Y: 90,
				},
				Max: image.Point{
					X: rgba.Bounds().Max.X,
					Y: 225,
				},
			},
			image.NewUniform(color.NRGBA{0, 0, 0, 128}),
			image.Point{},
			draw.Over,
		)
		// Draw the text.
		c.SetFont(bold)
		_, err = c.DrawString(textTop, freetype.Pt(rgba.Bounds().Max.X-posTop.X.Round()-50, 180))
		if err != nil {
			return nil, fmt.Errorf("drawing textTop: %w", err)
		}
	}

	if textBottom != "" {
		draw.Draw(
			rgba,
			image.Rectangle{
				Min: image.Point{
					X: 0,
					Y: height - 220,
				},
				Max: image.Point{
					X: posBottom.X.Round() + 100,
					Y: height - 85,
				},
			},
			image.NewUniform(color.NRGBA{0, 0, 0, 128}),
			image.Point{},
			draw.Over,
		)
		c.SetFont(regular)
		_, err = c.DrawString(textBottom, freetype.Pt(50, height-130))
		if err != nil {
			return nil, fmt.Errorf("drawing font: %w", err)
		}
	}

	r, w := io.Pipe()

	go func() {
		err := jpeg.Encode(w, rgba, nil)
		if err != nil {
			_ = w.CloseWithError(err)
		}
		_ = w.Close()
	}()

	return r, nil
}

func getFont(fname string) (*truetype.Font, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home dir: %w", err)
	}
	filePath := path.Join(home, ".config", "wildernessprime", fname)
	fontBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading font file: %w", err)
	}
	fontParsed, err := freetype.ParseFont(fontBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing font file: %w", err)
	}
	return fontParsed, nil
}
