package upload

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dave/youtube/resume"
	"google.golang.org/api/youtube/v3"
)

var MetaRegex = regexp.MustCompile(`\n{(.*)}$`)

/*
In October 2024, Siyuan and I hiked the Kanchenjunga Circuit Trail, following the footsteps of my first journey into this region at the start of the Great Himalaya Trail back in 2019, but heading east from Ghunsa over the beautiful Sele La to the stunning Ramche.

Attached is a CSV about my Youtube series, which documents the adventure in a vlog style (one video per day). The CSV includes the day number and the transcript of the video. It also includes the names and elevations of the camps and points of interest. If you mention any of these names in the output, please ensure you match the spelling in the input.

I'd like you to propose three things for each item:

* title: The Youtube video title... use best practises (e.g. optimal length 70 characters) to make this as compatible with the Youtube algorithm as possible in order to attract more views. Please ensure this doesn't exceed 100 characters.

* thumnbnail: A shorter string to be used on the thumbnail image... up to 30 characters max. Again, use best practises to make this attract more viewers.

* description: A description of the video for the Youtube description. This can be longer - up to a paragraph.

Please don't mention the day number, or the start / end locations in the text (I can add that with a template). If you need to refer to the people in the video, use terms like "me", "I" or "we".

Please return the output as a CSV with the following headers: key, title, thumbnail, description. Please keep blank rows where blank rows are included in the input data. Please use quotes so that the text is correctly escaped in CSV format. Please double-check that each item in your output corresponds exactly with one item in the input.


*/

/*
In May 2024, Siyuan and I attempted to hike around Nanga Parbat, the 9th tallest mountain in the world, from the Rupal valley in the south to Fairy Meadows in the north. I don't want to spoil the surprises, but not everything goes to plan, and that's an understatement.

Attached is a CSV about my Youtube series, which documents the adventure in a vlog style (one video per day). The CSV includes the day number and the transcript of the video.

I'd like you to propose three things for each item:

* title: The Youtube video title... use best practises (e.g. optimal length 70 characters) to make this as compatible with the Youtube algorithm as possible in order to attract more views. Please ensure this doesn't exceed 100 characters.

* thumnbnail: A shorter string to be used on the thumbnail image... up to 30 characters max. Again, use best practises to make this attract more viewers.

* description: A description of the video for the Youtube description. This can be longer - up to a paragraph.

Please don't mention the day number, or the start / end locations in the text (I can add that with a template). If you need to refer to the people in the video, use terms like "me", "I" or "we".

Please return the output as a CSV with the following headers: key, title, thumbnail, description. Please keep blank rows where blank rows are included in the input data. Please use quotes so that the text is correctly escaped in CSV format. Please double-check that each item in your output corresponds exactly with one item in the input.


*/

/*

In January 2020 I sailed on the Icebird Yacht from Argentina to the Antarctic Peninsular for a month of ski mountaineering. Attached is a CSV about my Youtube series, which documents the adventure in a vlog style (one video per day). The CSV includes the day number and the transcript of the video.

I'd like you to propose three things for each item:

* title: The Youtube video title... use best practises (e.g. optimal length 70 characters) to make this as compatible with the Youtube algorithm as possible in order to attract more views. Please ensure this doesn't exceed 100 characters.

* thumnbnail: A shorter string to be used on the thumbnail image... up to 30 characters max. Again, use best practises to make this attract more viewers.

* description: A description of the video for the Youtube description. This can be longer - up to a paragraph.

Please don't mention the day number, or the start / end locations in the text (I can add that with a template). If you need to refer to the people in the video, use terms like "me", "I" or "we".

Please return the output as a CSV with the following headers: key, title, thumbnail, description. Please keep blank rows where blank rows are included in the input data. Please use quotes so that the text is correctly escaped in CSV format.

*/

/*
Attached is a CSV about my Youtube series. The videos are vlog style (one per day), and the CSV includes the day number and the transcript of the video. The series documents my thru-hike of the Great Himalaya Trail (GHT). The concept of the Great Himalaya Trail is to follow the highest elevation continuous hiking route across the Himalayas. The Nepal section stretches for 1,400 km from Kanchenjunga in the east to Humla in the west. It winds through the mountains with an average elevation of 3,750 m, and up to 6,200 m, with an average daily ascent of over 1,000 m. The route includes parts of the more commercialised treks, linking them together with sections that are so remote even the locals seldom hike there.

I'd like you to propose for each item, three things:

* title: The Youtube video title... use best practises (e.g. optimal length 70 characters) to make this as compatible with the Youtube algorithm as possible in order to attract more views. Please ensure this doesn't exceed 100 characters.

* thumnbnail: A shorter string to be used on the thumbnail image... up to 30 characters max. Again, use best practises to make this attract more viewers.

* description: A description of the video for the Youtube description. This can be longer - up to a paragraph.

Please don't mention the day number, or the start / end locations in the text (I can add that with a template).

Please return the output as a CSV with the following headers: key, title, thumbnail, description. Please keep blank rows where blank rows are included in the input data.
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
			if err := item.Set(s, "transcript", out, false); err != nil {
				return fmt.Errorf("setting transcript (%v): %w", item.String(), err)
			}
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

	var videoIds []string
	itemsMap := map[string]*Item{}
	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		for _, item := range expedition.Items {
			if item.YoutubeId != "" {
				videoIds = append(videoIds, item.YoutubeId)
				itemsMap[item.YoutubeId] = item
			}
		}
	}

	apiPartsRead := []string{"snippet", "localizations", "status", "fileDetails"}

	const maxBatchSize = 50

	for i := 0; i < len(videoIds); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(videoIds) {
			end = len(videoIds)
		}

		fmt.Printf("Getting data for %d of %d videos\n", end-i, len(videoIds))

		response, err := s.YoutubeService.Videos.
			List(apiPartsRead).
			Id(videoIds[i:end]...).
			Do()
		if err != nil {
			return fmt.Errorf("youtube videos list call: %w", err)
		}
		if len(response.Items) != (end - i) {
			return fmt.Errorf("video list response length mismatch response: %d, request: %d)", len(response.Items), end-i)
		}
		for _, video := range response.Items {
			itemsMap[video.Id].YoutubeVideo = video
		}
	}

	return nil
}

type VideoMeta struct {
	Version    int    `json:"v"`
	Expedition string `json:"e"`
	Type       string `json:"t"`
	Key        int    `json:"k"`
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
		s.StoreVideoPreview(item, "video_tags", changes.Tags.Before, changes.Tags.After)
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
		s.StoreVideoPreview(item, "video_tags", "", changes.Tags.After)
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

		if err := item.Set(s, "youtube_id", insertedVideo.Id, false); err != nil {
			return fmt.Errorf("setting youtube_id (%v): %w", item.String(), err)
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

	fields.Tags = item.Tags

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
	Tags                 []string
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
	Changed                                            bool
	PrivacyStatus, PublishAt, Description, Title, Tags Change
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
		Tags:          Change{Before: strings.Join(video.Snippet.Tags, "\n")},
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

	if !slices.Equal(video.Snippet.Tags, y.Tags) {
		c.Changed = true
		video.Snippet.Tags = y.Tags
	}

	c.PrivacyStatus.After = video.Status.PrivacyStatus
	c.PublishAt.After = video.Status.PublishAt
	c.Description.After = video.Snippet.Description
	c.Title.After = video.Snippet.Title
	c.Tags.After = strings.Join(video.Snippet.Tags, "\n")

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
