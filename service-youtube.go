package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/youtube/v3"
)

const UPDATE = true
const INSERT = true

var ApiPartsList = []string{"snippet", "status"}
var ApiPartsRead = []string{"snippet", "localizations", "status", "fileDetails"}
var ApiPartsInsert = []string{"snippet", "localizations", "status"}
var ApiPartsUpdate = []string{"snippet", "localizations", "status"}

const YOUTUBE_CHANNEL_ID = "UCFDggPICIlCHp3iOWMYt8cg"

var MetaRegex = regexp.MustCompile(`\n{(.*)}$`)

type Meta struct {
	Version    int    `json:"v"`
	Expedition string `json:"e"`
	Type       string `json:"t"`
	Key        int    `json:"k"`
}

func (s *Service) InitialiseYoutubeAuthentication(ctx context.Context) error {

	// Read OAuth2 credentials from file
	// Create here: https://console.cloud.google.com/auth/clients?inv=1&invt=AbqgZQ&project=wildernessprime
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	oauth2Credentials, err := os.ReadFile(home + "/.config/wildernessprime/youtube-oauth2-client-secret.json")
	if err != nil {
		return fmt.Errorf("unable to read OAuth2 credentials file: %w", err)
	}

	config, err := google.ConfigFromJSON(
		oauth2Credentials,
		youtube.YoutubeReadonlyScope,
		"https://www.googleapis.com/auth/youtube.force-ssl",
	)
	if err != nil {
		return fmt.Errorf("unable to parse OAuth2 credentials file to config: %w", err)
	}

	token, err := getToken(config)
	if err != nil {
		return fmt.Errorf("unable to get token: %w", err)
	}

	client := config.Client(ctx, token)
	youtubeService, err := youtube.New(client)
	if err != nil {
		return fmt.Errorf("unable to create YouTube client: %w", err)
	}

	s.YoutubeService = youtubeService

	return nil
}

func (s *Service) GetVideosData() error {

	channelsResponse, _ := s.YoutubeService.Channels.
		List([]string{"contentDetails"}).
		Id(YOUTUBE_CHANNEL_ID).
		Do()
	uploadsPlaylistId := channelsResponse.Items[0].ContentDetails.RelatedPlaylists.Uploads

	var done bool
	var pageToken string

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

		fmt.Println("Got", len(playlistResponse.Items), "videos")
		var ids []string
		for _, item := range playlistResponse.Items {
			ids = append(ids, item.Snippet.ResourceId.VideoId)
		}

		videosResponse, err := s.YoutubeService.Videos.
			List(ApiPartsRead).
			Id(strings.Join(ids, ",")).
			Do()
		if err != nil {
			return fmt.Errorf("youtube videos list call: %w", err)
		}
		if len(videosResponse.Items) != len(playlistResponse.Items) {
			return fmt.Errorf("mismatch between playlistItems and videos")
		}

		for _, v := range videosResponse.Items {
			s.YoutubeVideos[v.Id] = v
		}

		pageToken = playlistResponse.NextPageToken
		if pageToken == "" {
			done = true
		}
	}
	return nil
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

		var meta Meta
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
		for _, current := range expedition.Items {
			if current.Type == meta.Type && current.Key == meta.Key {
				current.YoutubeId = video.Id
				current.YoutubeVideo = video
				break
			}
		}

	}
	return nil
}

func (s *Service) TestVideos() error {

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
				continue // no video uploaded
			}
			fields := DefaultYoutubeFields()

			fields.PublishAt = item.Release

			//bufDescription := &strings.Builder{}
			//if err := item.Expedition.Templates.ExecuteTemplate(bufDescription, item.Template, item); err != nil {
			//	return fmt.Errorf("error executing description template: %w", err)
			//}
			//metadata, err := item.Metadata()
			//if err != nil {
			//	return fmt.Errorf("error getting metadata: %w", err)
			//}
			//fields.Description = bufDescription.String() + "\n{" + metadata + "}"
			fields.Description = item.YoutubeVideo.Snippet.Description

			//bufTitle := &strings.Builder{}
			//if err := item.Expedition.Templates.ExecuteTemplate(bufTitle, "title", item); err != nil {
			//	return fmt.Errorf("error executing title template: %w", err)
			//}
			//fields.Title = bufTitle.String()
			fields.Title = item.YoutubeVideo.Snippet.Title

			if !fields.Equal(item.YoutubeVideo) {
				fmt.Println("Not equal")
			}

		}
	}
	return nil
}

func (s *Service) UpdateVideos() error {
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
				// skip any items that don't have youtube videos
				continue
			}
			changed, err := Apply(item, item.YoutubeVideo)
			if err != nil {
				return fmt.Errorf("applying data: %w", err)
			}
			if changed {
				if UPDATE {
					fmt.Printf("Updating video %s\n", item)

					// clear FileDetails because it's not updatable
					item.YoutubeVideo.FileDetails = nil

					if _, err := s.YoutubeService.Videos.Update(ApiPartsUpdate, item.YoutubeVideo).Do(); err != nil {
						return fmt.Errorf("updating video: %w", err)
					}
				} else {
					fmt.Printf("Skipped video update for %s\n", item)
				}
			}
		}
	}
	return nil
}

func (s *Service) CreateVideos() error {
	// find all missing videos which need to be created
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
			if item.YoutubeVideo != nil {
				// skip any items that already have youtube videos
				continue
			}

			video := &youtube.Video{}

			_, err := Apply(item, video)
			if err != nil {
				return fmt.Errorf("applying data: %w", err)
			}

			if INSERT {
				fmt.Printf("Inserting video %s\n", item)

				call := s.YoutubeService.Videos.Insert(ApiPartsInsert, video)

				download, err := s.DriveService.Files.Get(item.VideoFile.Id).Download()
				if err != nil {
					return fmt.Errorf("downloading drive file: %w", err)
				}

				insertCall := call.Media(download.Body)

				call.ProgressUpdater(func(current, total int64) {
					fmt.Printf(" - uploaded %d of %d bytes (%.2f%%)\n", current, download.ContentLength, float64(current)/float64(download.ContentLength)*100)
				})

				insertCall.Header().Add("Slug", item.VideoFile.Name)

				if _, err := insertCall.Do(); err != nil {
					_ = download.Body.Close() // ignore error
					return fmt.Errorf("inserting video: %w", err)
				}
				_ = download.Body.Close() // ignore error
			} else {
				fmt.Printf("Skipped video insert for %s\n", item)
			}
		}
	}
	return nil
}

func Apply(item *Item, video *youtube.Video) (changed bool, err error) {

	fields := DefaultYoutubeFields()

	fields.PublishAt = item.Release

	bufDescription := &strings.Builder{}
	if err := item.Expedition.Templates.ExecuteTemplate(bufDescription, item.Template, item); err != nil {
		return false, fmt.Errorf("error executing description template: %w", err)
	}
	metadata, err := item.Metadata()
	if err != nil {
		return false, fmt.Errorf("error getting metadata: %w", err)
	}
	fields.Description = bufDescription.String() + "\n\n{" + metadata + "}"

	bufTitle := &strings.Builder{}
	if err := item.Expedition.Templates.ExecuteTemplate(bufTitle, "title", item); err != nil {
		return false, fmt.Errorf("error executing title template: %w", err)
	}
	fields.Title = bufTitle.String()

	if fields.Equal(video) {
		return false, nil
	}

	fields.Apply(video)

	return true, nil
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

func (y *YoutubeFields) Apply(video *youtube.Video) {
	if video.Status == nil {
		video.Status = &youtube.VideoStatus{}
	}

	if time.Now().After(y.PublishAt) {
		video.Status.PrivacyStatus = "public"
		video.Status.PublishAt = ""
	} else {
		video.Status.PrivacyStatus = y.PrivacyStatus
		video.Status.PublishAt = timeToYoutube(y.PublishAt)
	}

	if video.Snippet == nil {
		video.Snippet = &youtube.VideoSnippet{}
	}
	video.Snippet.CategoryId = y.CategoryId
	video.Snippet.ChannelId = y.ChannelId
	video.Snippet.DefaultAudioLanguage = y.DefaultAudioLanguage
	video.Snippet.DefaultLanguage = y.DefaultLanguage
	video.Snippet.LiveBroadcastContent = y.LiveBroadcastContent
	video.Snippet.Description = y.Description
	video.Snippet.Title = y.Title
}

func (y *YoutubeFields) Equal(video *youtube.Video) bool {
	if video.Status == nil {
		return false
	}

	if time.Now().After(y.PublishAt) {
		// video should be public, if not public then it is not equal
		if video.Status.PrivacyStatus != "public" {
			return false
		}
		// no need to compare PublishAt - once it is released, this is blank.
	} else {
		if video.Status.PrivacyStatus != y.PrivacyStatus {
			return false
		}
		if video.Status.PublishAt != timeToYoutube(y.PublishAt) {
			return false
		}
	}

	if video.Snippet == nil {
		return false
	}
	if video.Snippet.CategoryId != y.CategoryId {
		return false
	}
	if video.Snippet.ChannelId != y.ChannelId {
		return false
	}
	if video.Snippet.DefaultAudioLanguage != y.DefaultAudioLanguage {
		return false
	}
	if video.Snippet.DefaultLanguage != y.DefaultLanguage {
		return false
	}
	if video.Snippet.LiveBroadcastContent != y.LiveBroadcastContent {
		return false
	}
	if video.Snippet.Description != y.Description {
		return false
	}
	if video.Snippet.Title != y.Title {
		return false
	}
	return true
}

func timeToYoutube(t time.Time) string {
	return strings.TrimSuffix(t.Format(time.RFC3339), "Z") + ".0Z"
}
