package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/edwvee/exiffix"
	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/youtube/v3"
)

var ApiPartsList = []string{"snippet", "status"}
var ApiPartsRead = []string{"snippet", "localizations", "status", "fileDetails"}
var ApiPartsInsert = []string{"snippet", "localizations", "status"}
var ApiPartsUpdate = []string{"snippet", "localizations", "status"}

const YOUTUBE_CHANNEL_ID = "UCFDggPICIlCHp3iOWMYt8cg"

var MetaRegex = regexp.MustCompile(`\n{(.*)}$`)

type VideoMeta struct {
	Version    int    `json:"v"`
	Expedition string `json:"e"`
	Type       string `json:"t"`
	Key        int    `json:"k"`
}

type PlaylistMeta struct {
	Version    int    `json:"v"`
	Expedition string `json:"e"`
	Section    string `json:"s"`
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
			List(ApiPartsRead).
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
			List(ApiPartsRead).
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

func (s *Service) listPlaylistsItems(playlistId string) ([]*youtube.PlaylistItem, error) {
	var done bool
	var pageToken string
	var totalResults int64
	var items []*youtube.PlaylistItem
	itemsByVideoId := map[string]*youtube.PlaylistItem{}

	for !done {
		playlistResponse, err := s.YoutubeService.PlaylistItems.
			List([]string{"snippet"}).
			PlaylistId(playlistId).
			MaxResults(50).
			PageToken(pageToken).
			Do()
		if err != nil {
			return nil, fmt.Errorf("youtube playlistItems list call: %w", err)
		}
		totalResults = playlistResponse.PageInfo.TotalResults

		fmt.Println("Got", len(playlistResponse.Items), "of", totalResults, "playlist items")
		for _, item := range playlistResponse.Items {
			items = append(items, item)
			itemsByVideoId[item.Snippet.ResourceId.VideoId] = item
		}

		pageToken = playlistResponse.NextPageToken
		if pageToken == "" {
			done = true
		}
	}
	if totalResults != int64(len(itemsByVideoId)) {
		return nil, fmt.Errorf("only found %d unique playlist items (should be %d)", len(itemsByVideoId), totalResults)
	}
	return items, nil
}

func (s *Service) GetPlaylistsData() error {

	var done bool
	var pageToken string
	var totalResults int64

	for !done {
		playlistResponse, err := s.YoutubeService.Playlists.
			List([]string{"snippet"}).
			ChannelId(YOUTUBE_CHANNEL_ID).
			MaxResults(50).
			PageToken(pageToken).
			Do()
		if err != nil {
			return fmt.Errorf("youtube playlists list call: %w", err)
		}
		totalResults = playlistResponse.PageInfo.TotalResults

		fmt.Println("Got", len(playlistResponse.Items), "of", totalResults, "playlists")
		// https://issuetracker.google.com/issues/402138565
		for _, p := range playlistResponse.Items {
			s.YoutubePlaylists[p.Id] = p
		}

		pageToken = playlistResponse.NextPageToken
		if pageToken == "" {
			done = true
		}
	}

	if totalResults != int64(len(s.YoutubePlaylists)) {
		return fmt.Errorf("only found %d playlists (should be %d)", len(s.YoutubePlaylists), totalResults)
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

func (s *Service) ParsePlaylistsMetaData() error {
	special := map[string]bool{
		"PLiM-TFJI81R-fbq9vC9vQo_PVuys01WJo": true, // Antarctica
		"PLiM-TFJI81R_X4HUrRDjwSJmK-MpqC1dW": true, // The Great Himalaya Trail
	}
	for _, playlist := range s.YoutubePlaylists {
		matches := MetaRegex.FindStringSubmatch(playlist.Snippet.Description)

		if len(matches) == 0 {
			// ignore existing videos uploaded before metadata was added
			if _, ok := special[playlist.Id]; !ok {
				return fmt.Errorf("no meta data found for %s", playlist.Id)
			}
		}

		var meta PlaylistMeta

		if special[playlist.Id] {
			switch playlist.Id {
			case "PLiM-TFJI81R_X4HUrRDjwSJmK-MpqC1dW":
				meta = PlaylistMeta{
					Version:    1,
					Expedition: "ght",
				}
			case "PLiM-TFJI81R-fbq9vC9vQo_PVuys01WJo":
				meta = PlaylistMeta{
					Version:    1,
					Expedition: "ant",
				}
			}
		} else {
			metaBase64 := matches[1]
			metaJson, err := base64.StdEncoding.DecodeString(metaBase64)
			if err != nil {
				return fmt.Errorf("decoding playlist meta data for %s: %w", playlist.Id, err)
			}
			if err := json.Unmarshal(metaJson, &meta); err != nil {
				return fmt.Errorf("unmarshaling playlist meta data for %s: %w", playlist.Id, err)
			}
		}

		expedition, ok := s.Expeditions[meta.Expedition]
		if !ok {
			return fmt.Errorf("expedition %s not found", meta.Expedition)
		}
		if !expedition.Process {
			continue
		}
		if meta.Section == "" {
			expedition.PlaylistId = playlist.Id
			expedition.Playlist = playlist
			continue
		}
		for _, section := range expedition.Sections {
			if section.Ref == meta.Section {
				section.PlaylistId = playlist.Id
				section.Playlist = playlist
				break
			}
		}
	}
	return nil
}

func (s *Service) CreateOrUpdateVideos() error {
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
				if err := s.createVideo(item); err != nil {
					return fmt.Errorf("updating video: %w", err)
				}
			} else {
				if err := s.updateVideo(item); err != nil {
					return fmt.Errorf("updating video: %w", err)
				}
			}
		}
	}
	return nil
}

func (s *Service) updateVideo(item *Item) error {
	changes, err := Apply(item, item.YoutubeVideo)
	if err != nil {
		return fmt.Errorf("applying data: %w", err)
	}
	if s.Global.Preview {
		// store updated metadata
		s.StoreVideoPreview(item, "video_title", changes.Title.Before, changes.Title.After)
		s.StoreVideoPreview(item, "video_description", changes.Description.Before, changes.Description.After)
		s.StoreVideoPreview(item, "video_privacy_status", changes.PrivacyStatus.Before, changes.PrivacyStatus.After)
		s.StoreVideoPreview(item, "video_publish_at", changes.PublishAt.Before, changes.PublishAt.After)
	} else {
		if changes.Changed {
			fmt.Printf("Updating video %s\n", item)
			// clear FileDetails because it's not updatable
			item.YoutubeVideo.FileDetails = nil
			if _, err := s.YoutubeService.Videos.Update(ApiPartsUpdate, item.YoutubeVideo).Do(); err != nil {
				return fmt.Errorf("updating video: %w", err)
			}
		}
	}
	if item.Expedition.Thumbnails {
		if err := updateThumbnail(s, item); err != nil {
			return fmt.Errorf("updating thumbnails in insert: %w", err)
		}
	}
	return nil
}

func (s *Service) createVideo(item *Item) error {
	video := &youtube.Video{}

	changes, err := Apply(item, video)
	if err != nil {
		return fmt.Errorf("applying data: %w", err)
	}

	if s.Global.Preview {
		s.StoreVideoPreview(item, "video_title", "", changes.Title.After)
		s.StoreVideoPreview(item, "video_description", "", changes.Description.After)
		s.StoreVideoPreview(item, "video_privacy_status", "", changes.PrivacyStatus.After)
		s.StoreVideoPreview(item, "video_publish_at", "", changes.PublishAt.After)
	} else {
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

		insertedVideo, err := insertCall.Do()
		if err != nil {
			_ = download.Body.Close() // ignore error
			return fmt.Errorf("inserting video: %w", err)
		}
		_ = download.Body.Close() // ignore error

		item.YoutubeVideo = insertedVideo
		item.YoutubeId = insertedVideo.Id

		if item.Expedition.Thumbnails {
			if err := updateThumbnail(s, item); err != nil {
				return fmt.Errorf("updating thumbnails in insert: %w", err)
			}
		}
	}
	return nil
}

func (s *Service) CreateOrUpdatePlaylists() error {
	// find all the playlists which need to be updated
	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		if !expedition.Ready {
			continue
		}
		if expedition.ExpeditionPlaylist {
			if expedition.Playlist == nil {
				// create playlist
				if err := s.createPlaylist(expedition); err != nil {
					return fmt.Errorf("creating expedition playlist: %w", err)
				}
			} else {
				if err := s.updatePlaylist(expedition); err != nil {
					return fmt.Errorf("updating expedition playlist: %w", err)
				}
			}
		} else {
			if expedition.Playlist != nil {
				if err := s.deletePlaylist(expedition); err != nil {
					return fmt.Errorf("deleting expedition playlist: %w", err)
				}
			}
		}
		if expedition.SectionPlaylists {
			for _, section := range expedition.Sections {
				if section.Playlist == nil {
					if err := s.createPlaylist(section); err != nil {
						return fmt.Errorf("creating section playlist: %w", err)
					}
				} else {
					if err := s.updatePlaylist(section); err != nil {
						return fmt.Errorf("updating section playlist: %w", err)
					}
				}
			}
		} else {
			for _, section := range expedition.Sections {
				if section.Playlist != nil {
					if err := s.deletePlaylist(section); err != nil {
						return fmt.Errorf("deleting section playlist: %w", err)
					}
				}
			}
		}
	}
	return nil
}

func (s *Service) getPlaylistDetails(parent HasPlaylist) (title, description string, content []*Item, err error) {
	expedition := parent.GetExpedition()

	templateData := map[string]any{}
	templateData["Expedition"] = expedition
	section, ok := parent.(*Section)
	if ok {
		templateData["Section"] = section
	}

	titleBuffer := bytes.NewBufferString("")
	if err := expedition.Templates.ExecuteTemplate(titleBuffer, "playlist_title", templateData); err != nil {
		return "", "", nil, fmt.Errorf("execute playlists_title template: %w", err)
	}
	title = titleBuffer.String()
	descBuffer := bytes.NewBufferString("")
	if err := expedition.Templates.ExecuteTemplate(descBuffer, "playlist_description", templateData); err != nil {
		return "", "", nil, fmt.Errorf("execute playlists_desc template: %w", err)
	}
	metadata, err := parent.GetMetadata()
	if err != nil {
		return "", "", nil, fmt.Errorf("error getting playlist metadata: %w", err)
	}
	description = strings.TrimSpace(descBuffer.String()) + "\n\n{" + metadata + "}"

	allItems := parent.GetItems()
	for _, item := range allItems {
		if !item.Video {
			continue
		}
		if !item.Ready {
			continue
		}
		if item.YoutubeVideo == nil {
			continue
		}
		content = append(content, item)
	}
	return title, description, content, nil
}

func (s *Service) updatePlaylist(parent HasPlaylist) error {
	playlist := parent.GetPlaylist()
	title, description, content, err := s.getPlaylistDetails(parent)
	if err != nil {
		return fmt.Errorf("getting playlist details: %w", err)
	}

	if s.Global.Preview {
		s.StorePlaylistPreview(parent, "playlist_title", playlist.Snippet.Title, title)
		s.StorePlaylistPreview(parent, "playlist_description", playlist.Snippet.Description, description)
	} else {
		if title != playlist.Snippet.Title || description != playlist.Snippet.Description {
			// update playlist
			playlist.Snippet.Title = title
			playlist.Snippet.Description = description
			playlist.Snippet.DefaultLanguage = "en"
			if _, err := s.YoutubeService.Playlists.Update(ApiPartsUpdate, playlist).Do(); err != nil {
				return fmt.Errorf("updating playlist: %w", err)
			}
		}
	}

	playlistItems, err := s.listPlaylistsItems(parent.GetPlaylistId())
	if err != nil {
		return fmt.Errorf("listing playlist items: %w", err)
	}
	var changed bool
	if len(playlistItems) != len(content) {
		changed = true
	} else {
		for i, item := range content {
			if playlistItems[i].Snippet.ResourceId.VideoId != item.YoutubeId {
				changed = true
				break
			}
		}
	}
	if changed {
		// sync the youtube playlist
		if err := s.syncPlaylist(parent, content, playlistItems); err != nil {
			return fmt.Errorf("syncing playlist: %w", err)
		}
	} else {
		if s.Global.Preview {
			s.StorePlaylistPreviewOps(parent, "playlist_content", nil)
		}
	}
	return nil
}

// Compute LCS between input and output (by YoutubeId)
func lcs(input []*Item, output []*youtube.PlaylistItem) []*Item {
	n, m := len(input), len(output)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}

	// Fill LCS DP table
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if input[i-1].YoutubeId == output[j-1].Snippet.ResourceId.VideoId {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				if dp[i-1][j] > dp[i][j-1] {
					dp[i][j] = dp[i-1][j]
				} else {
					dp[i][j] = dp[i][j-1]
				}
			}
		}
	}

	// Backtrack to reconstruct LCS
	i, j := n, m
	var lcsList []*Item
	for i > 0 && j > 0 {
		if input[i-1].YoutubeId == output[j-1].Snippet.ResourceId.VideoId {
			lcsList = append([]*Item{input[i-1]}, lcsList...)
			i--
			j--
		} else if dp[i-1][j] > dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	return lcsList
}

func (s *Service) syncPlaylist(parent HasPlaylist, input []*Item, output []*youtube.PlaylistItem) error {
	playlistId := parent.GetPlaylistId()
	var ops []string
	lcsList := lcs(input, output)

	// Step 1: Delete items in output that are NOT in LCS
	lcsSet := make(map[string]bool)
	for _, v := range lcsList {
		lcsSet[v.YoutubeId] = true
	}

	// Delete unwanted items
	for i, item := range output {
		videoId := item.Snippet.ResourceId.VideoId
		if !lcsSet[videoId] {
			if s.Global.Preview {
				ops = append(ops, fmt.Sprintf("delete at %d (%s)", i, videoId))
			} else {
				fmt.Printf("Deleting playlist item: %s\n", videoId)
				if err := s.YoutubeService.PlaylistItems.Delete(item.Id).Do(); err != nil {
					return fmt.Errorf("failed to delete video %s: %v", videoId, err)
				}
			}
		}
	}

	// Step 2: Insert missing videos **at correct positions**
	lcsIndex := 0
	outputIndex := 0

	for _, v := range input {
		if lcsIndex < len(lcsList) && lcsList[lcsIndex].YoutubeId == v.YoutubeId {
			lcsIndex++
			outputIndex++ // Advance for existing items
		} else {
			if s.Global.Preview {
				ops = append(ops, fmt.Sprintf("insert at %d (%s)", outputIndex, v.YoutubeId))
			} else {
				fmt.Printf("Inserting playlist item: %s at position %d\n", v.YoutubeId, outputIndex)
				_, err := s.YoutubeService.PlaylistItems.Insert([]string{"snippet"}, &youtube.PlaylistItem{
					Snippet: &youtube.PlaylistItemSnippet{
						PlaylistId: playlistId,
						ResourceId: &youtube.ResourceId{
							Kind:    "youtube#video",
							VideoId: v.YoutubeId,
						},
						Position: int64(outputIndex), // Insert at correct position
					},
				}).Do()
				if err != nil {
					return fmt.Errorf("failed to insert video %s: %v", v.YoutubeId, err)
				}
			}
			outputIndex++ // Advance since we inserted
		}
	}
	if s.Global.Preview {
		s.StorePlaylistPreviewOps(parent, "playlist_content", ops)
	}

	return nil
}

func (s *Service) createPlaylist(parent HasPlaylist) error {
	title, description, content, err := s.getPlaylistDetails(parent)
	if err != nil {
		return fmt.Errorf("getting playlist details: %w", err)
	}

	if s.Global.Preview {
		s.StorePlaylistPreview(parent, "playlist_title", "", title)
		s.StorePlaylistPreview(parent, "playlist_description", "", description)
		var ops []string
		for _, item := range content {
			ops = append(ops, fmt.Sprintf("insert (%s)", item.YoutubeId))
		}
		s.StorePlaylistPreviewOps(parent, "playlist_content", ops)
	} else {
		playlist := &youtube.Playlist{
			Snippet: &youtube.PlaylistSnippet{
				Title:           title,
				Description:     description,
				DefaultLanguage: "en",
			},
			Status: &youtube.PlaylistStatus{
				PrivacyStatus: "public", // or "private" or "unlisted"
			},
		}
		fmt.Println("Creating playlist")
		call := s.YoutubeService.Playlists.Insert(
			[]string{"snippet", "status"},
			playlist,
		)
		newPlaylist, err := call.Do()
		if err != nil {
			return fmt.Errorf("creating playlist: %w", err)
		}
		switch parent := parent.(type) {
		case *Expedition:
			parent.PlaylistId = newPlaylist.Id
			parent.Playlist = newPlaylist
		case *Section:
			parent.PlaylistId = newPlaylist.Id
			parent.Playlist = newPlaylist
		}

		for _, item := range content {
			playlistItem := &youtube.PlaylistItem{
				Snippet: &youtube.PlaylistItemSnippet{
					PlaylistId: newPlaylist.Id,
					ResourceId: &youtube.ResourceId{
						Kind:    "youtube#video",
						VideoId: item.YoutubeId,
					},
					//Position: i, // do we need this?
				},
			}
			fmt.Println("Creating playlist item for", item.YoutubeId)
			if _, err := s.YoutubeService.PlaylistItems.Insert([]string{"snippet"}, playlistItem).Do(); err != nil {
				return fmt.Errorf("inserting playlist item: %w", err)
			}
		}
	}
	return nil
}

func (s *Service) deletePlaylist(parent HasPlaylist) error {
	playlist := parent.GetPlaylist()
	if s.Global.Preview {
		s.StorePlaylistPreviewDeleted(parent)
	} else {
		fmt.Println("Deleting playlist", parent.GetPlaylistId())
		if err := s.YoutubeService.Playlists.Delete(playlist.Id).Do(); err != nil {
			return fmt.Errorf("deleting playlist: %w", err)
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

	if s.Global.Preview {
		s.StoreVideoPreview(item, "thumbnail_top", "", textTopBuffer.String())
		s.StoreVideoPreview(item, "video_description", "", textBottomBuffer.String())
	} else {
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

func Apply(item *Item, video *youtube.Video) (changes Changes, err error) {

	fields := DefaultYoutubeFields()

	fields.PublishAt = item.Release

	bufDescription := &strings.Builder{}
	if err := item.Expedition.Templates.ExecuteTemplate(bufDescription, item.Template, item); err != nil {
		return Changes{}, fmt.Errorf("error executing description template: %w", err)
	}
	metadata, err := item.Metadata()
	if err != nil {
		return Changes{}, fmt.Errorf("error getting metadata: %w", err)
	}
	fields.Description = strings.TrimSpace(bufDescription.String()) + "\n\n{" + metadata + "}"

	bufTitle := &strings.Builder{}
	if err := item.Expedition.Templates.ExecuteTemplate(bufTitle, "title", item); err != nil {
		return Changes{}, fmt.Errorf("error executing title template: %w", err)
	}
	fields.Title = bufTitle.String()

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

//func (y *YoutubeFields) Equal(video *youtube.Video) bool {
//	if video.Status == nil {
//		return false
//	}
//
//	if time.Now().After(y.PublishAt) {
//		// video should be public, if not public then it is not equal
//		if video.Status.PrivacyStatus != "public" {
//			return false
//		}
//		// no need to compare PublishAt - once it is released, this is blank.
//	} else {
//		if video.Status.PrivacyStatus != y.PrivacyStatus {
//			return false
//		}
//		if video.Status.PublishAt != timeToYoutube(y.PublishAt) {
//			return false
//		}
//	}
//
//	if video.Snippet == nil {
//		return false
//	}
//	if video.Snippet.CategoryId != y.CategoryId {
//		return false
//	}
//	if video.Snippet.ChannelId != y.ChannelId {
//		return false
//	}
//	if video.Snippet.DefaultAudioLanguage != y.DefaultAudioLanguage {
//		return false
//	}
//	if video.Snippet.DefaultLanguage != y.DefaultLanguage {
//		return false
//	}
//	if video.Snippet.LiveBroadcastContent != y.LiveBroadcastContent {
//		return false
//	}
//	if video.Snippet.Description != y.Description {
//		return false
//	}
//	if video.Snippet.Title != y.Title {
//		return false
//	}
//	return true
//}

func timeToYoutube(t time.Time) string {
	return strings.TrimSuffix(t.Format(time.RFC3339), "Z") + ".0Z"
}

type HasPlaylist interface {
	GetPlaylistId() string
	GetPlaylist() *youtube.Playlist
	GetExpedition() *Expedition
	GetItems() []*Item
	GetMetadata() (string, error)
}

func (e *Expedition) GetPlaylistId() string {
	return e.PlaylistId
}
func (e *Expedition) GetPlaylist() *youtube.Playlist {
	return e.Playlist
}
func (e *Expedition) GetExpedition() *Expedition {
	return e
}
func (e *Expedition) GetItems() []*Item {
	return e.Items
}
func (e *Expedition) GetMetadata() (string, error) {
	metaData := PlaylistMeta{
		Version:    1,
		Expedition: e.Ref,
	}
	metaDataBytes, err := json.Marshal(metaData)
	if err != nil {
		return "", fmt.Errorf("encoding expedition playlist meta data json: %w", err)
	}
	return base64.StdEncoding.EncodeToString(metaDataBytes), nil
}
func (s *Section) GetPlaylistId() string {
	return s.PlaylistId
}
func (s *Section) GetPlaylist() *youtube.Playlist {
	return s.Playlist
}
func (s *Section) GetExpedition() *Expedition {
	return s.Expedition
}
func (s *Section) GetItems() []*Item {
	return s.Items
}
func (s *Section) GetMetadata() (string, error) {
	metaData := PlaylistMeta{
		Version:    1,
		Expedition: s.Expedition.Ref,
		Section:    s.Ref,
	}
	metaDataBytes, err := json.Marshal(metaData)
	if err != nil {
		return "", fmt.Errorf("encoding section playlist meta data json: %w", err)
	}
	return base64.StdEncoding.EncodeToString(metaDataBytes), nil
}
