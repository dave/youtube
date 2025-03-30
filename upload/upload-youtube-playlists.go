package upload

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/api/youtube/v3"
)

func (s *Service) GetPlaylistsData() error {

	var done bool
	var pageToken string
	var totalResults int64

	for !done {
		playlistResponse, err := s.YoutubeService.Playlists.
			List([]string{"snippet"}).
			ChannelId(s.ChannelId).
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

type PlaylistMeta struct {
	Version    int    `json:"v"`
	Expedition string `json:"e"`
	Section    string `json:"s"`
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
				return fmt.Errorf("decoding playlist meta data (%v): %w", playlist.Id, err)
			}
			if err := json.Unmarshal(metaJson, &meta); err != nil {
				return fmt.Errorf("unmarshaling playlist meta data (%v): %w", playlist.Id, err)
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

func (s *Service) CreateOrUpdatePlaylists() error {
	// find all the playlists which need to be updated
	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		if expedition.ExpeditionPlaylist {
			if expedition.Playlist == nil {
				// create playlist
				if err := s.createPlaylist(expedition); err != nil {
					return fmt.Errorf("creating expedition playlist (%v): %w", expedition.Ref, err)
				}
			} else {
				if err := s.updatePlaylist(expedition); err != nil {
					return fmt.Errorf("updating expedition playlist (%v): %w", expedition.Ref, err)
				}
			}
		} else {
			if expedition.Playlist != nil {
				if err := s.deletePlaylist(expedition); err != nil {
					return fmt.Errorf("deleting expedition playlist (%v): %w", expedition.Ref, err)
				}
			}
		}
		if expedition.SectionPlaylists {
			for _, section := range expedition.Sections {
				if section.Playlist == nil {
					if err := s.createPlaylist(section); err != nil {
						return fmt.Errorf("creating section playlist (%v, %v): %w", expedition.Ref, section.Ref, err)
					}
				} else {
					if err := s.updatePlaylist(section); err != nil {
						return fmt.Errorf("updating section playlist (%v, %v): %w", expedition.Ref, section.Ref, err)
					}
				}
			}
		} else {
			for _, section := range expedition.Sections {
				if section.Playlist != nil {
					if err := s.deletePlaylist(section); err != nil {
						return fmt.Errorf("deleting section playlist (%v, %v): %w", expedition.Ref, section.Ref, err)
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
		return "", "", nil, fmt.Errorf("execute playlists_title template (%v): %w", parent.String(), err)
	}
	title = titleBuffer.String()
	descBuffer := bytes.NewBufferString("")
	if err := expedition.Templates.ExecuteTemplate(descBuffer, "playlist_description", templateData); err != nil {
		return "", "", nil, fmt.Errorf("execute playlists_desc template (%v): %w", parent.String(), err)
	}
	metadata, err := parent.GetMetadata()
	if err != nil {
		return "", "", nil, fmt.Errorf("error getting playlist metadata (%v): %w", parent.String(), err)
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
		return fmt.Errorf("getting playlist details (%v): %w", parent.String(), err)
	}

	if s.Global.Preview {
		s.StorePlaylistPreview(parent, "playlist_title", playlist.Snippet.Title, title)
		s.StorePlaylistPreview(parent, "playlist_description", playlist.Snippet.Description, description)
	}
	if s.Global.Production {
		if title != playlist.Snippet.Title || description != playlist.Snippet.Description {
			// update playlist
			playlist.Snippet.Title = title
			playlist.Snippet.Description = description
			playlist.Snippet.DefaultLanguage = "en"
			parts := []string{"snippet", "localizations", "status"}
			if _, err := s.YoutubeService.Playlists.Update(parts, playlist).Do(); err != nil {
				return fmt.Errorf("updating playlist (%v): %w", parent.String(), err)
			}
		}
	}

	playlistItems, err := s.listPlaylistsItems(parent.GetPlaylistId())
	if err != nil {
		return fmt.Errorf("listing playlist items (%v): %w", parent.String(), err)
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
			return fmt.Errorf("syncing playlist (%v): %w", parent.String(), err)
		}
	} else {
		if s.Global.Preview {
			s.StorePlaylistPreviewOps(parent, "playlist_content", nil)
		}
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
			}
			if s.Global.Production {
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
			}
			if s.Global.Production {
				fmt.Printf("Inserting playlist item: %s at position %d\n", v.YoutubeId, outputIndex)
				pli, err := s.YoutubeService.PlaylistItems.Insert([]string{"snippet"}, &youtube.PlaylistItem{
					Snippet: &youtube.PlaylistItemSnippet{
						PlaylistId: playlistId,
						ResourceId: &youtube.ResourceId{
							Kind:    "youtube#video",
							VideoId: v.YoutubeId,
						},
					},
				}).Do()
				if err != nil {
					return fmt.Errorf("failed to insert playlist (%v) item %s: %w", parent.String(), v.YoutubeId, err)
				}
				// Position is ignored when inserting, must do an update fix.
				pli.Snippet.Position = int64(outputIndex)
				if _, err := s.YoutubeService.PlaylistItems.Update([]string{"snippet"}, pli).Do(); err != nil {
					return fmt.Errorf("failed to update playlist (%v) item %s: %w", parent.String(), pli.Id, err)
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
		return fmt.Errorf("getting playlist details (%v): %w", parent.String(), err)
	}

	if s.Global.Preview {
		s.StorePlaylistPreview(parent, "playlist_title", "", title)
		s.StorePlaylistPreview(parent, "playlist_description", "", description)
		var ops []string
		for _, item := range content {
			ops = append(ops, fmt.Sprintf("insert (%s)", item.YoutubeId))
		}
		s.StorePlaylistPreviewOps(parent, "playlist_content", ops)
	}
	if s.Global.Production {
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
			return fmt.Errorf("creating playlist (%v): %w", parent.String(), err)
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
				return fmt.Errorf("inserting playlist item (%v): %w", parent.String(), err)
			}
		}
	}
	return nil
}

func (s *Service) deletePlaylist(parent HasPlaylist) error {
	playlist := parent.GetPlaylist()
	if s.Global.Preview {
		s.StorePlaylistPreviewDeleted(parent)
	}
	if s.Global.Production {
		fmt.Println("Deleting playlist", parent.GetPlaylistId())
		if err := s.YoutubeService.Playlists.Delete(playlist.Id).Do(); err != nil {
			return fmt.Errorf("deleting playlist (%v): %w", parent.String(), err)
		}
	}
	return nil
}

type HasPlaylist interface {
	GetPlaylistId() string
	GetPlaylist() *youtube.Playlist
	GetExpedition() *Expedition
	GetItems() []*Item
	GetMetadata() (string, error)
	String() string
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
func (e *Expedition) String() string {
	return fmt.Sprintf("%v", e.Ref)
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
func (s *Section) String() string {
	return fmt.Sprintf("%v, %v", s.Expedition.Ref, s.Ref)
}
