package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"os"

	"github.com/disintegration/imaging"
	"github.com/edwvee/exiffix"
	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"google.golang.org/api/drive/v3"
)

func (s *Service) UpdateThumbnails() error {
	// find all the videos which need to be updated
	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		if !expedition.Ready {
			continue
		}
		if !expedition.Thumbnails {
			continue
		}
		for _, item := range expedition.Items {
			if !item.Video {
				continue
			}
			if !item.Ready {
				continue
			}
			if item.YoutubeVideo == nil {
				continue
			}
			if err := updateThumbnail(s, item); err != nil {
				return fmt.Errorf("updating thumbnail: %w", err)
			}
		}
	}
	return nil
}

func updateThumbnail(s *Service, item *Item) error {

	textTopBuffer := bytes.NewBufferString("")
	if err := item.Expedition.Templates.ExecuteTemplate(textTopBuffer, "thumbnail_top", item); err != nil {
		return fmt.Errorf("execute thumbnail top template: %w", err)
	}
	textBottomBuffer := bytes.NewBufferString("")
	if err := item.Expedition.Templates.ExecuteTemplate(textBottomBuffer, "thumbnail_bottom", item); err != nil {
		return fmt.Errorf("execute thumbnail top template: %w", err)
	}

	fmt.Println("Updating thumbnail", item.String())
	download, err := s.DriveService.Files.Get(item.ThumbnailFile.Id).Download()
	if err != nil {
		return fmt.Errorf("downloading drive file: %w", err)
	}
	f, err := transformImage(download.Body, textTopBuffer.String(), textBottomBuffer.String())
	if err != nil {
		_ = download.Body.Close()
		return fmt.Errorf("transforming thumbnail: %w", err)
	}
	_ = download.Body.Close()

	if s.Global.Preview {
		s.StoreVideoPreview(item, "thumbnail_top", "", textTopBuffer.String())
		s.StoreVideoPreview(item, "thumbnail_bottom", "", textBottomBuffer.String())
		fileMetadata := &drive.File{
			Name:    item.String(),
			Parents: []string{s.Global.PreviewThumbnailsFolder},
		}
		if _, err := s.DriveService.Files.Create(fileMetadata).Media(f).Do(); err != nil {
			return fmt.Errorf("creating file: %w", err)
		}
	}
	if s.Global.Production {
		if _, err := s.YoutubeService.Thumbnails.Set(item.YoutubeVideo.Id).Media(f).Do(); err != nil {
			return fmt.Errorf("setting thumbnail: %w", err)
		}
	}
	return nil

	// write to file for debugging
	//thumbnailFile, err := os.Create("thumbnail.jpg")
	//if err != nil {
	//	return fmt.Errorf("creating thumbnail file: %w", err)
	//}
	//_, err = io.Copy(thumbnailFile, f)
	//if err != nil {
	//	return fmt.Errorf("writing thumbnail file: %w", err)
	//}
	//return fmt.Errorf("stopping here")

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
	fontBytes, err := os.ReadFile(home + "/.config/wildernessprime/" + fname)
	if err != nil {
		return nil, fmt.Errorf("reading font file: %w", err)
	}
	fontParsed, err := freetype.ParseFont(fontBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing font file: %w", err)
	}
	return fontParsed, nil
}
