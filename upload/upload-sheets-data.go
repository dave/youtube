package upload

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox/files"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/sheets/v4"
	"google.golang.org/api/youtube/v3"
)

type Sheet struct {
	Spreadsheet *sheets.Spreadsheet
	Name        string
	Expedition  *Expedition
	Headers     []string
	Data        []map[string]Cell
	DataByRef   map[string]map[string]Cell
}

func (s *Sheet) Set(service *sheets.Service, rowId int, column string, value any, force bool) error {

	// find column in headers
	columnId := -1
	for id, header := range s.Headers {
		if header == column {
			columnId = id
		}
	}
	if columnId == -1 {
		return fmt.Errorf("column %v not found in headers", column)
	}
	cellRange := getCellRange(columnId+1, rowId)

	if s.Name == "" {
		return fmt.Errorf("sheet has no name")
	}

	sheetRange := fmt.Sprintf("%s!%s", s.Name, cellRange)
	if !force {
		// Read the current value of the cell
		resp, err := service.Spreadsheets.Values.Get(s.Spreadsheet.SpreadsheetId, sheetRange).Do()
		if err != nil {
			return fmt.Errorf("unable to retrieve data from sheet: %w", err)
		}

		// Check if the cell has contents
		if len(resp.Values) > 0 && len(resp.Values[0]) > 0 && resp.Values[0][0] != "" {
			return fmt.Errorf("cell %v is not empty", column)
		}
	}

	fmt.Printf("Updating cell %v (%v) in %v\n", cellRange, column, s.Name)

	// Update the cell with the new value
	valueRange := &sheets.ValueRange{
		Values: [][]interface{}{{value}},
	}
	_, err := service.Spreadsheets.Values.Update(s.Spreadsheet.SpreadsheetId, sheetRange, valueRange).ValueInputOption("RAW").Do()
	if err != nil {
		return fmt.Errorf("unable to update cell: %w", err)
	}

	dataId := -1
	for i, d := range s.Data {
		if dd, ok := d["row_id"]; ok && dd.Int() == rowId {
			dataId = i
			break
		}
	}
	if dataId == -1 {
		return fmt.Errorf("item with row_id %v not found in %v", rowId, s.Name)
	}
	s.Data[dataId][column] = Cell{value}
	if ref, found := s.Data[dataId]["ref"]; found {
		s.DataByRef[ref.String()][column] = Cell{value}
	}

	return nil
}

func (s *Sheet) Clear(service *sheets.Service, rowId int, column string) error {

	// find column in headers
	columnId := -1
	for id, header := range s.Headers {
		if header == column {
			columnId = id
		}
	}
	if columnId == -1 {
		return fmt.Errorf("column %v not found in headers", column)
	}
	cellRange := getCellRange(columnId+1, rowId)

	if s.Name == "" {
		return fmt.Errorf("sheet has no name")
	}

	sheetRange := fmt.Sprintf("%s!%s", s.Name, cellRange)

	fmt.Printf("Clearing cell %v (%v) in %v\n", cellRange, column, s.Name)

	_, err := service.Spreadsheets.Values.Clear(s.Spreadsheet.SpreadsheetId, sheetRange, &sheets.ClearValuesRequest{}).Do()
	if err != nil {
		return fmt.Errorf("unable to clear cell: %w", err)
	}

	dataId := -1
	for i, d := range s.Data {
		if dd, ok := d["row_id"]; ok && dd.Int() == rowId {
			dataId = i
			break
		}
	}
	if dataId == -1 {
		return fmt.Errorf("item with row_id %v not found in %v", rowId, s.Name)
	}
	s.Data[dataId][column] = Cell{nil}
	if ref, found := s.Data[dataId]["ref"]; found {
		s.DataByRef[ref.String()][column] = Cell{nil}
	}

	return nil
}

type Global struct {
	Preview                  bool
	Production               bool
	Thumbnails               bool
	Titles                   bool
	PreviewThumbnailsFolder  string
	PreviewThumbnailsDropbox string
	Data                     map[string]Cell
}

type Expedition struct {
	RowId              int
	Ref                string
	Name               string
	Process            bool
	VideosFolder       string
	ThumbnailsFolder   string
	VideosDropbox      string
	ThumbnailsDropbox  string
	ExpeditionPlaylist bool
	SectionPlaylists   bool
	DataSheetId        string
	Spreadsheet        *sheets.Spreadsheet
	Sheets             map[string]*Sheet
	Data               map[string]Cell
	SectionsByRef      map[string]*Section
	Sections           []*Section
	Items              []*Item
	Templates          *template.Template
	PlaylistId         string
	Playlist           *youtube.Playlist
	ItemSheet          *Sheet
}

func (e *Expedition) HasThumbnails() bool {
	for _, item := range e.Items {
		if item.DoThumbnail {
			return true
		}
	}
	return false
}

type Section struct {
	RowId      int
	Expedition *Expedition
	Items      []*Item
	Ref        string
	Name       string
	Data       map[string]Cell
	PlaylistId string
	Playlist   *youtube.Playlist
}

type Item struct {
	RowId                int
	Type                 string
	Key                  int
	Video                bool
	Template             string
	Ready                bool
	DoThumbnail          bool
	Release              time.Time
	From, To             Location
	Via                  []Location
	Section              *Section
	SectionRef           string
	Expedition           *Expedition
	Data                 map[string]Cell
	VideoGoogleDrive     *drive.File
	ThumbnailGoogleDrive *drive.File
	VideoDropbox         *files.FileMetadata
	ThumbnailDropbox     *files.FileMetadata
	YoutubeId            string
	YoutubeVideo         *youtube.Video
	YoutubeTranscript    string
	Tags                 []string
}

type Location struct {
	Name      string
	Elevation int
}

func (item *Item) Metadata() (string, error) {
	metaData := VideoMeta{
		Version:    1,
		Expedition: item.Expedition.Ref,
		Type:       item.Type,
		Key:        item.Key,
	}
	metaDataBytes, err := json.Marshal(metaData)
	if err != nil {
		return "", fmt.Errorf("encoding youtube meta data json: %w", err)
	}
	return base64.StdEncoding.EncodeToString(metaDataBytes), nil
}

func (item *Item) String() string {
	if item.Section != nil {
		return fmt.Sprintf("%s, %s, %s, %d", item.Expedition.Ref, item.Type, item.Section.Ref, item.Key)
	}
	return fmt.Sprintf("%s, %s, %d", item.Expedition.Ref, item.Type, item.Key)
}

type Cell struct{ Value any }

func (c Cell) String() string {
	switch v := c.Value.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%v", v)
	case int:
		return fmt.Sprintf("%v", v)
	case bool:
		if v {
			return "true"
		} else {
			return "false"
		}
	case nil:
		return ""
	default:
		return ""
	}
}

func (c Cell) Time() time.Time {
	if c.Float() == 0 {
		return time.Time{}
	}
	// Google Sheets base date is December 30, 1899
	baseDate := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	// Add the number of days (including fractional days) to the base date
	return baseDate.Add(time.Duration(c.Float() * 24 * float64(time.Hour)))
}

func (c Cell) Float() float64 {
	switch v := c.Value.(type) {
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}
		return f
	case float64:
		return v
	case int:
		return float64(v)
	case bool:
		if v {
			return 1
		} else {
			return 0
		}
	case nil:
		return 0
	default:
		return 0
	}
}

func (c Cell) Int() int {
	switch v := c.Value.(type) {
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}
		return int(math.Round(f))
	case float64:
		return int(math.Round(v))
	case int:
		return v
	case bool:
		if v {
			return 1
		} else {
			return 0
		}
	case nil:
		return 0
	default:
		return 0
	}
}

func (c Cell) Bool() bool {
	switch v := c.Value.(type) {
	case string:
		return strings.ToLower(v) == "true"
	case float64:
		return v == 1
	case int:
		return v == 1
	case bool:
		return v
	case nil:
		return false
	default:
		return false
	}
}

func (c Cell) Empty() bool {
	switch v := c.Value.(type) {
	case string:
		return v == ""
	case float64:
		return false
	case int:
		return false
	case bool:
		return false
	case nil:
		return true
	default:
		return false
	}
}

func (c Cell) Nil() bool {
	switch c.Value.(type) {
	case string:
		return false
	case float64:
		return false
	case int:
		return false
	case bool:
		return false
	case nil:
		return true
	default:
		return false
	}
}

func (c Cell) Zero() bool {
	switch v := c.Value.(type) {
	case string:
		return v == ""
	case float64:
		return v == 0
	case int:
		return v == 0
	case bool:
		return v == false
	case nil:
		return true
	default:
		return false
	}
}
