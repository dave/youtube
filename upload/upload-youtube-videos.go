package upload

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dave/youtube/resume"
	"google.golang.org/api/youtube/v3"
)

var MetaRegex = regexp.MustCompile(`\n{(.*)}$`)

func (s *Service) GetVideosData() error {

	apiPartsRead := []string{"snippet", "localizations", "status", "fileDetails"}

	channelsResponse, _ := s.YoutubeService.Channels.
		List([]string{"contentDetails"}).
		Id(s.ChannelId).
		Do()
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
		if !expedition.Ready {
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
	if s.Global.Production {
		if changes.Changed {
			fmt.Printf("Updating video %s\n", item)
			// clear FileDetails because it's not updatable
			item.YoutubeVideo.FileDetails = nil
			parts := []string{"snippet", "localizations", "status"}
			if _, err := s.YoutubeService.Videos.Update(parts, item.YoutubeVideo).Do(); err != nil {
				return fmt.Errorf("updating video (%v): %w", item.String(), err)
			}
		}
	}

	return nil
}

func (s *Service) createVideo(ctx context.Context, item *Item) error {

	res, err := s.getResume()
	if err != nil {
		return fmt.Errorf("getting uploader (%v): %w", item.String(), err)
	}
	if res.State == resume.StateUploadInProgress {
		return fmt.Errorf("upload already in progress")
	}
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
	if s.Global.Production {
		fmt.Printf("Uploading video %s\n", item)
		progress := func(start int64) {
			fmt.Printf(" - uploaded %d of %d bytes (%.2f%%)\n", start, res.ContentLength, float64(start)/float64(res.ContentLength)*100)
		}
		if err := res.Initialise(item.VideoFile.Id, video); err != nil {
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
		if video.Status.PublishAt != timeToYoutube(y.PublishAt) {
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
