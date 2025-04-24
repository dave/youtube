package upload

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/dave/youtube/resume"
	"google.golang.org/api/youtube/v3"
)

var MetaRegex = regexp.MustCompile(`\n{(.*)}$`)

/*
Attached is a CSV about the first few days of a 5 month hike in Nepal. Each day has a Youtube video, vlog style. Included are the start and end locations, elevations and the transcript of the Youtube video. I'd like you to propose for each item, three things:

title: The Youtube video title... use best practises (e.g. optimal length 70 characters) to make this as compatible with the Youtube algorithm as possible in order to attract more videos. Please ensure this doesn't exceed 100 characters.

thumnbnail: A shorter string to be used on the thumbnail video... up to 30 characters max. Again, use best practises to make this attract more viewers.

description: A description of the video for the Youtube description. This can be longer - up to a paragraph.

Please don't mention the day number, or the start / end locations in the text (I can add that).
*/

func (s *Service) GetVideosCaptions() error {
	var downloaded int
	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		for _, item := range expedition.Items {
			if item.YoutubeVideo == nil {
				continue
			}
			if item.YoutubeTranscript != "" {
				continue
			}
			if downloaded >= 20 {
				fmt.Println("Downloaded 20 captions, stopping")
				return nil
			}
			fmt.Println("Getting captions for", item.String())
			captionsListResponse, err := s.YoutubeService.Captions.List([]string{"id", "snippet"}, item.YoutubeId).Do()
			if err != nil {
				return fmt.Errorf("youtube captions list call (%v): %w", item.String(), err)
			}
			if len(captionsListResponse.Items) == 0 {
				return fmt.Errorf("no captions found (%v)", item.String())
			}
			var captionsId string
			for _, caption := range captionsListResponse.Items {
				if caption.Snippet.Language == "en" {
					captionsId = caption.Id
					break
				}
			}
			if captionsId == "" {
				//return fmt.Errorf("no english captions found (%v)", item.String())
				fmt.Printf("Could not find english captions (%v), using %s instead.\n", item.String(), captionsListResponse.Items[0].Snippet.Language)
				captionsId = captionsListResponse.Items[0].Id
			}
			captionsDownloadResponse, err := s.YoutubeService.Captions.Download(captionsId).Tfmt("ttml").Download()
			if err != nil {
				return fmt.Errorf("youtube captions download call (%v): %w", item.String(), err)
			}
			b, err := io.ReadAll(captionsDownloadResponse.Body)
			if err != nil {
				return fmt.Errorf("reading captions download response (%v): %w", item.String(), err)
			}
			elements, err := extractPElements(b)
			if err != nil {
				return fmt.Errorf("extracting P elements (%v): %w", item.String(), err)
			}
			var out string
			for _, element := range elements {
				if element == "[Music]" {
					continue
				}
				out += " " + element
			}
			out = strings.TrimSpace(out)
			if out == "" {
				out = "[None]"
			}
			item.YoutubeTranscript = out
			downloaded++
		}
	}
	return nil
}

type P struct {
	XMLName xml.Name `xml:"p"`
	Content string   `xml:",chardata"`
}

func extractPElements(body []byte) ([]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	var pElements []string
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error decoding XML: %w", err)
		}
		switch se := tok.(type) {
		case xml.StartElement:
			if se.Name.Local == "p" {
				var p P
				if err := decoder.DecodeElement(&p, &se); err != nil {
					return nil, fmt.Errorf("error decoding element: %w", err)
				}
				pElements = append(pElements, p.Content)
			}
		}
	}
	return pElements, nil
}

func (s *Service) GetVideosData() error {

	apiPartsRead := []string{"snippet", "localizations", "status", "fileDetails"}

	channelsResponse, err := s.YoutubeService.Channels.
		List([]string{"contentDetails"}).
		Id(s.ChannelId).
		Do()
	if err != nil {
		return fmt.Errorf("youtube channels list call: %w", err)
	}
	uploadsPlaylistId := channelsResponse.Items[0].ContentDetails.RelatedPlaylists.Uploads

	var done bool
	var pageToken string
	var totalResults int64

	for !done {
		playlistResponse, err := s.YoutubeService.PlaylistItems.
			List([]string{"snippet"}).
			PlaylistId(uploadsPlaylistId).
			MaxResults(50).
			PageToken(pageToken).
			Do()
		if err != nil {
			return fmt.Errorf("youtube playlistItems list call: %w", err)
		}
		totalResults = playlistResponse.PageInfo.TotalResults

		fmt.Println("Got", len(playlistResponse.Items), "of", totalResults, "videos")
		var ids []string
		for _, item := range playlistResponse.Items {
			ids = append(ids, item.Snippet.ResourceId.VideoId)
		}

		videosResponse, err := s.YoutubeService.Videos.
			List(apiPartsRead).
			Id(strings.Join(ids, ",")).
			Do()
		if err != nil {
			return fmt.Errorf("youtube videos list call: %w", err)
		}
		if len(videosResponse.Items) != len(playlistResponse.Items) {
			return fmt.Errorf("mismatch between playlistItems and videos")
		}

		// https://issuetracker.google.com/issues/402138565
		for _, v := range videosResponse.Items {
			s.YoutubeVideos[v.Id] = v
		}

		pageToken = playlistResponse.NextPageToken
		if pageToken == "" {
			done = true
		}
	}

	if _, ok := s.YoutubeVideos["POHhwrogJ8U"]; !ok {
		// one video is always missing from results
		// https://issuetracker.google.com/issues/402138565
		missingVideoResponse, err := s.YoutubeService.Videos.
			List(apiPartsRead).
			Id("POHhwrogJ8U").
			Do()
		if err != nil {
			return fmt.Errorf("youtube videos list call: %w", err)
		}
		if len(missingVideoResponse.Items) != 1 {
			return fmt.Errorf("missing video not found")
		}
		v := missingVideoResponse.Items[0]
		s.YoutubeVideos[v.Id] = v
	}
	if totalResults != int64(len(s.YoutubeVideos)) {
		return fmt.Errorf("only found %d videos (should be %d)", len(s.YoutubeVideos), totalResults)
	}

	return nil
}

type VideoMeta struct {
	Version    int    `json:"v"`
	Expedition string `json:"e"`
	Type       string `json:"t"`
	Key        int    `json:"k"`
}

func (s *Service) ParseVideosMetaData() error {
	for _, video := range s.YoutubeVideos {
		matches := MetaRegex.FindStringSubmatch(video.Snippet.Description)

		if len(matches) == 0 {
			// ignore existing videos uploaded before metadata was added
			switch video.Id {
			case "aghBgeKEsR4",
				"lbGWiVMW49c",
				"UzJZLKhTc58",
				"Y6rY1eoqASA",
				"HMxIWQIjeN8",
				"Q4ZN62I38Yc":
				continue
			}
			return fmt.Errorf("no meta data found for %s", video.Id)
		}

		metaBase64 := matches[1]

		metaJson, err := base64.StdEncoding.DecodeString(metaBase64)
		if err != nil {
			return fmt.Errorf("decoding youtube meta data for %s: %w", video.Id, err)
		}

		var meta VideoMeta
		if err := json.Unmarshal(metaJson, &meta); err != nil {
			return fmt.Errorf("unmarshaling youtube meta data for %s: %w", video.Id, err)
		}

		expedition, ok := s.Expeditions[meta.Expedition]
		if !ok {
			return fmt.Errorf("expedition %s not found", meta.Expedition)
		}
		if !expedition.Process {
			continue
		}
		for _, item := range expedition.Items {
			if item.Type == meta.Type && item.Key == meta.Key {
				item.YoutubeId = video.Id
				item.YoutubeVideo = video
				break
			}
		}

	}
	return nil
}

func (s *Service) CreateOrUpdateVideos(ctx context.Context) error {
	// find all the videos which need to be updated
	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		for _, item := range expedition.Items {
			if !item.Video {
				continue
			}
			if item.YoutubeVideo == nil {
				// video doesn't exist yet, create it
				if err := s.createVideo(ctx, item); err != nil {
					return fmt.Errorf("updating video (%v): %w", item.String(), err)
				}
			} else {
				if err := s.updateVideo(item); err != nil {
					return fmt.Errorf("updating video (%v): %w", item.String(), err)
				}
			}
		}
	}
	return nil
}

func (s *Service) updateVideo(item *Item) error {

	changes, err := Apply(item, item.YoutubeVideo)
	if err != nil {
		return fmt.Errorf("applying data (%v): %w", item.String(), err)
	}

	if s.Global.Preview {
		// store updated metadata
		s.StoreVideoPreview(item, "video_title", changes.Title.Before, changes.Title.After)
		s.StoreVideoPreview(item, "video_description", changes.Description.Before, changes.Description.After)
		s.StoreVideoPreview(item, "video_privacy_status", changes.PrivacyStatus.Before, changes.PrivacyStatus.After)
		s.StoreVideoPreview(item, "video_publish_at", changes.PublishAt.Before, changes.PublishAt.After)
	}
	if s.Global.Production && item.Ready && changes.Changed {
		fmt.Printf("Updating video (%v)\n", item.String())
		// clear FileDetails because it's not updatable
		item.YoutubeVideo.FileDetails = nil
		parts := []string{"snippet", "localizations", "status"}
		if _, err := s.YoutubeService.Videos.Update(parts, item.YoutubeVideo).Do(); err != nil {
			return fmt.Errorf("updating video (%v): %w", item.String(), err)
		}
	}

	return nil
}

func (s *Service) createVideo(ctx context.Context, item *Item) error {

	video := &youtube.Video{}

	changes, err := Apply(item, video)
	if err != nil {
		return fmt.Errorf("applying data (%v): %w", item.String(), err)
	}

	if s.Global.Preview {
		s.StoreVideoPreview(item, "video_title", "", changes.Title.After)
		s.StoreVideoPreview(item, "video_description", "", changes.Description.After)
		s.StoreVideoPreview(item, "video_privacy_status", "", changes.PrivacyStatus.After)
		s.StoreVideoPreview(item, "video_publish_at", "", changes.PublishAt.After)
	}
	if s.Global.Production && item.Ready {

		res, err := s.getResume()
		if err != nil {
			return fmt.Errorf("getting uploader (%v): %w", item.String(), err)
		}
		if res.State == resume.StateUploadInProgress {
			return fmt.Errorf("upload already in progress (%v)", item.String())
		}

		fmt.Printf("Uploading video (%s)\n", item.String())
		progress := func(start int64) {
			fmt.Printf(" - uploaded %d of %d bytes (%.2f%%)\n", start, res.ContentLength, float64(start)/float64(res.ContentLength)*100)
		}
		var videoFileId string
		switch s.StorageService {
		case GoogleDriveStorage:
			videoFileId = item.VideoGoogleDrive.Id
		case DropboxStorage:
			videoFileId = item.VideoDropbox.Id
		}
		if err := res.Initialise(videoFileId, video); err != nil {
			return fmt.Errorf("initialising upload (%v): %w", item.String(), err)
		}
		insertedVideo, err := res.Upload(ctx, progress)
		if err != nil {
			return fmt.Errorf("uploading video (%v): %w", item.String(), err)
		}

		item.YoutubeVideo = insertedVideo
		item.YoutubeId = insertedVideo.Id

	}

	return nil
}

func apply(item *Item) (YoutubeFields, error) {

	fields := DefaultYoutubeFields()

	fields.PublishAt = item.Release

	bufDescription := &strings.Builder{}
	if err := item.Expedition.Templates.ExecuteTemplate(bufDescription, item.Template, item); err != nil {
		return YoutubeFields{}, fmt.Errorf("error executing description template (%v): %w", item.String(), err)
	}
	metadata, err := item.Metadata()
	if err != nil {
		return YoutubeFields{}, fmt.Errorf("error getting metadata (%v): %w", item.String(), err)
	}
	fields.Description = strings.TrimSpace(bufDescription.String()) + "\n\n{" + metadata + "}"

	bufTitle := &strings.Builder{}
	if err := item.Expedition.Templates.ExecuteTemplate(bufTitle, "title", item); err != nil {
		return YoutubeFields{}, fmt.Errorf("error executing title template (%v): %w", item.String(), err)
	}
	fields.Title = bufTitle.String()

	return fields, nil
}

func Apply(item *Item, video *youtube.Video) (changes Changes, err error) {
	fields, err := apply(item)
	if err != nil {
		return Changes{}, fmt.Errorf("applying data (%v): %w", item.String(), err)
	}
	return fields.Apply(video), nil
}

type YoutubeFields struct {
	PrivacyStatus        string    // privacy status before PublishAt time. After this time, it is always public.
	PublishAt            time.Time // no default
	CategoryId           string
	ChannelId            string
	DefaultAudioLanguage string
	DefaultLanguage      string
	LiveBroadcastContent string
	Description          string // no default
	Title                string // no default
}

func DefaultYoutubeFields() YoutubeFields {
	return YoutubeFields{
		PrivacyStatus:        "private",
		CategoryId:           "19",
		ChannelId:            "UCFDggPICIlCHp3iOWMYt8cg",
		DefaultAudioLanguage: "en",
		DefaultLanguage:      "en",
		LiveBroadcastContent: "none",
	}
}

type Change struct {
	Before, After string
}
type Changes struct {
	Changed                                      bool
	PrivacyStatus, PublishAt, Description, Title Change
}

func (y *YoutubeFields) Apply(video *youtube.Video) Changes {

	if video.Status == nil {
		video.Status = &youtube.VideoStatus{}
	}

	if video.Snippet == nil {
		video.Snippet = &youtube.VideoSnippet{}
	}

	c := Changes{
		PrivacyStatus: Change{Before: video.Status.PrivacyStatus},
		PublishAt:     Change{Before: video.Status.PublishAt},
		Description:   Change{Before: video.Snippet.Description},
		Title:         Change{Before: video.Snippet.Title},
	}

	if time.Now().After(y.PublishAt) {
		if video.Status.PrivacyStatus != "public" {
			c.Changed = true
			video.Status.PrivacyStatus = "public"
		}
		if video.Status.PublishAt != "" {
			c.Changed = true
			video.Status.PublishAt = ""
		}
	} else {
		if video.Status.PrivacyStatus != y.PrivacyStatus {
			c.Changed = true
			video.Status.PrivacyStatus = y.PrivacyStatus
		}
		if !youtubeTimeEqual(video.Status.PublishAt, y.PublishAt) {
			c.Changed = true
			video.Status.PublishAt = timeToYoutube(y.PublishAt)
		}
	}
	if video.Snippet.CategoryId != y.CategoryId {
		c.Changed = true
		video.Snippet.CategoryId = y.CategoryId
	}
	if video.Snippet.ChannelId != y.ChannelId {
		c.Changed = true
		video.Snippet.ChannelId = y.ChannelId
	}
	if video.Snippet.DefaultAudioLanguage != y.DefaultAudioLanguage {
		c.Changed = true
		video.Snippet.DefaultAudioLanguage = y.DefaultAudioLanguage
	}
	if video.Snippet.DefaultLanguage != y.DefaultLanguage {
		c.Changed = true
		video.Snippet.DefaultLanguage = y.DefaultLanguage
	}
	if video.Snippet.LiveBroadcastContent != y.LiveBroadcastContent {
		c.Changed = true
		video.Snippet.LiveBroadcastContent = y.LiveBroadcastContent
	}
	if video.Snippet.Description != y.Description {
		c.Changed = true
		video.Snippet.Description = y.Description
	}
	if video.Snippet.Title != y.Title {
		c.Changed = true
		video.Snippet.Title = y.Title
	}

	c.PrivacyStatus.After = video.Status.PrivacyStatus
	c.PublishAt.After = video.Status.PublishAt
	c.Description.After = video.Snippet.Description
	c.Title.After = video.Snippet.Title

	return c
}

func timeToYoutube(t time.Time) string {
	return strings.TrimSuffix(t.Format(time.RFC3339), "Z") + ".0Z"
}

func youtubeTimeEqual(s string, t time.Time) bool {
	t1, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return false
	}
	return t.Equal(t1)
}
