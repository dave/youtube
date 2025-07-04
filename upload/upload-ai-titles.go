package upload

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	"google.golang.org/api/sheets/v4"
	"google.golang.org/genai"
)

func (s *Service) GenerateAiTitles(ctx context.Context) error {

	if !s.Global.Titles {
		return nil
	}

	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		request := &GeminiRequest{
			Name:        expedition.Name,
			Description: expedition.Data["description"].String(),
		}
		for _, item := range expedition.Items {
			geminiItem := GeminiRequestItem{
				Type:        item.Type,
				Section:     item.SectionRef,
				Key:         item.Key,
				Title:       item.Data["title"].String(),
				Thumbnail:   item.Data["thumbnail"].String(),
				Description: item.Data["description"].String(),
				Tags:        strings.Split(item.Data["tags"].String(), "\n"),
			}
			if item.YoutubeTranscript != "" && item.YoutubeTranscript != "[None]" {
				var landmarks []string
				if item.From.Name != "" {
					landmarks = append(landmarks, fmt.Sprintf("%s", item.From.Name))
				}
				if item.To.Name != "" && item.From.Name != item.To.Name {
					landmarks = append(landmarks, fmt.Sprintf("%s", item.To.Name))
				}
				for _, location := range item.Via {
					landmarks = append(landmarks, fmt.Sprintf("%s", location.Name))
				}
				geminiItem.Landmarks = strings.Join(landmarks, ", ")
				geminiItem.Transcript = item.YoutubeTranscript
			}
			request.Items = append(request.Items, geminiItem)
		}
		requestMarshalled, err := json.Marshal(request)
		if err != nil {
			return fmt.Errorf("failed to marshal Gemini request: %w", err)
		}

		fmt.Printf("Generating AI titles for %s...\n", expedition.Ref)

		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("getting home dir: %w", err)
		}
		filePath := path.Join(home, ".config", "wildernessprime", "gemini-api.key")
		apiKeyBytes, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("unable to read gemini api key: %w", err)
		}
		apiKey := strings.TrimSpace(string(apiKeyBytes))

		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:  apiKey,
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			return fmt.Errorf("generating gemini client: %w", err)
		}

		config := &genai.GenerateContentConfig{
			ResponseMIMEType: "application/json",
			ResponseSchema: &genai.Schema{
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"type":        {Type: genai.TypeString},
						"section":     {Type: genai.TypeString},
						"key":         {Type: genai.TypeInteger},
						"title":       {Type: genai.TypeString},
						"thumbnail":   {Type: genai.TypeString},
						"tags":        {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
						"description": {Type: genai.TypeString},
					},
					PropertyOrdering: []string{"type", "section", "key", "title", "thumbnail", "tags", "description"},
				},
			},
		}

		query := s.Global.Data["ai_prompt"].String() + `

I'll be expecting the response to be in the correct format, so please double check the response matches this: 

[
  {
    "type": "",
    "section": "",
    "key": 0,
    "title": "",
    "description": "",
    "thumbnail": ""
  },
  ...
]

Here's the data about the videos:

` + "```\n" + string(requestMarshalled) + "\n```\n"

		result, err := client.Models.GenerateContent(
			ctx,
			"gemini-2.5-pro-preview-05-06",
			genai.Text(query),
			config,
		)
		if err != nil {
			return fmt.Errorf("getting gemini response: %w", err)
		}
		fmt.Printf("Saving AI titles to preview_titles sheet...\n")

		var results []GeminiResponseItem
		if err := json.Unmarshal([]byte(result.Text()), &results); err != nil {
			return fmt.Errorf("unmarshaling results %#v: %w", result.Text(), err)
		}
		var values [][]any
		for _, resultItem := range results {
			var item *Item
			for _, current := range expedition.Items {
				if resultItem.Key == current.Key && resultItem.Type == current.Type && resultItem.Section == current.SectionRef {
					item = current
					break
				}
			}
			if item == nil {
				return fmt.Errorf("no item found for %#v", resultItem)
			}
			// headers := []string{"expedition", "type", "key", "title", "thumbnail", "tags", "description"}
			var value []any
			value = append(value, item.Expedition.Ref)
			value = append(value, item.Type)
			value = append(value, item.Key)
			value = append(value, resultItem.Title)
			value = append(value, resultItem.Thumbnail)
			value = append(value, strings.ToLower(strings.Join(resultItem.Tags, "\n")))
			value = append(value, resultItem.Description)
			values = append(values, value)
		}
		// Define the range to append the data
		rangeToAppend := fmt.Sprintf("%s!A1", "preview_titles")

		// Create a request to append the specified values
		valueRange := &sheets.ValueRange{
			Values: values,
		}

		// Execute the append request
		_, err = s.SheetsService.Spreadsheets.Values.Append(SPREADSHEET_ID, rangeToAppend, valueRange).
			ValueInputOption("RAW").
			InsertDataOption("INSERT_ROWS").
			Do()
		if err != nil {
			return fmt.Errorf("unable to append rows to preview_titles sheet: %w", err)
		}
	}

	return nil
}

type GeminiRequest struct {
	Name        string              `json:"title"`       // expedition.name
	Description string              `json:"description"` // expedition.description
	Items       []GeminiRequestItem `json:"items"`
}

type GeminiRequestItem struct {
	Type        string   `json:"type"`
	Section     string   `json:"section"`
	Key         int      `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Thumbnail   string   `json:"thumbnail"`
	Tags        []string `json:"tags"`
	Landmarks   string   `json:"landmarks"`
	Transcript  string   `json:"transcript"`
}

type GeminiResponseItem struct {
	Type        string   `json:"type"`
	Section     string   `json:"section"`
	Key         int      `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Thumbnail   string   `json:"thumbnail"`
	Tags        []string `json:"tags"`
}
