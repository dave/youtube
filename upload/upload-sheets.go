package upload

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/akedrou/textdiff"
	"google.golang.org/api/sheets/v4"
)

const SPREADSHEET_ID = "1e2gK0GgWN4PxeZcazUvxtlhYGzg2lZsZEkphqu9Jplc"

func (s *Service) InitSheetsService() error {
	sheetsService, err := sheets.New(s.ServiceAccountClient)
	if err != nil {
		return fmt.Errorf("unable to retrieve Sheets client: %w", err)
	}
	s.SheetsService = sheetsService

	spreadsheet, err := s.SheetsService.Spreadsheets.Get(SPREADSHEET_ID).Do()
	if err != nil {
		return fmt.Errorf("unable to retrieve spreadsheets: %w", err)
	}
	s.Spreadsheet = spreadsheet
	return nil
}

func (s *Service) ClearPreviewSheets() error {
	if s.Global.Preview {
		fmt.Println("Clearing preview sheet")

		// clear "preview_videos", "preview_titles" sheet, but leave first row (headers)
		_, err := s.SheetsService.Spreadsheets.Values.Clear(
			SPREADSHEET_ID,
			fmt.Sprintf("%s!2:1000", "preview_videos"),
			&sheets.ClearValuesRequest{},
		).Do()
		if err != nil {
			return fmt.Errorf("unable to clear preview_videos sheet data: %w", err)
		}
	}

	if s.Global.Titles {
		fmt.Println("Clearing preview titles sheet")

		_, err := s.SheetsService.Spreadsheets.Values.Clear(
			SPREADSHEET_ID,
			fmt.Sprintf("%s!2:1000", "preview_titles"),
			&sheets.ClearValuesRequest{},
		).Do()
		if err != nil {
			return fmt.Errorf("unable to clear preview_titles sheet data: %w", err)
		}
	}
	return nil
}

func (s *Service) GetAllSheetsData() error {
	for _, sheetData := range s.Spreadsheet.Sheets {
		if strings.HasPrefix(sheetData.Properties.Title, "_") {
			continue
		}
		if strings.HasPrefix(sheetData.Properties.Title, "Copy of") {
			continue
		}
		skip := map[string]bool{
			"global":            true,
			"expedition":        true,
			"preview_videos":    true,
			"preview_playlists": true,
		}
		if skip[sheetData.Properties.Title] {
			continue
		}
		if err := s.GetSheetData(nil, sheetData.Properties.Title); err != nil {
			return fmt.Errorf("unable to get sheet data (%v): %w", sheetData.Properties.Title, err)
		}
	}
	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		if err := s.GetSheets(expedition); err != nil {
			return fmt.Errorf("unable to get expedition sheets: %w", err)
		}
	}
	return nil
}

func (s *Service) GetSheets(expedition *Expedition) error {
	spreadsheet, err := s.SheetsService.Spreadsheets.Get(expedition.DataSheetId).Do()
	if err != nil {
		return fmt.Errorf("unable to retrieve spreadsheets: %w", err)
	}
	expedition.Spreadsheet = spreadsheet

	for _, sheet := range expedition.Spreadsheet.Sheets {
		if err := s.GetSheetData(expedition, sheet.Properties.Title); err != nil {
			return fmt.Errorf("unable to get sheet data (%v): %w", sheet.Properties.Title, err)
		}
	}

	return nil
}

func (s *Service) GetSheetData(expedition *Expedition, titles ...string) error {
	for _, title := range titles {
		sheet := &Sheet{
			DataByRef: map[string]map[string]Cell{},
		}
		sheet.Name = title
		if expedition != nil {
			sheet.Expedition = expedition
			sheet.Spreadsheet = expedition.Spreadsheet
			expedition.Sheets[sheet.Name] = sheet
			if sheet.Name == "item" {
				expedition.ItemSheet = sheet
			}
		} else {
			sheet.Spreadsheet = s.Spreadsheet
			s.Sheets[sheet.Name] = sheet
		}

		if expedition != nil {
			fmt.Printf("Getting raw data for sheet %s (%s)\n", title, expedition.Ref)
		} else {
			fmt.Printf("Getting raw data for sheet %s\n", title)
		}

		values, err := s.SheetsService.Spreadsheets.Values.
			Get(sheet.Spreadsheet.SpreadsheetId, title).
			ValueRenderOption("UNFORMATTED_VALUE").
			Do()
		if err != nil {
			return fmt.Errorf("unable to retrieve values from sheet (%v): %w", title, err)
		}
		hasRef := false
		refColumn := 0
		for i, row := range values.Values {
			if i == 0 {
				for columnIndex, header := range row {
					if header.(string) == "ref" {
						hasRef = true
						refColumn = columnIndex
					}
					sheet.Headers = append(sheet.Headers, header.(string))
				}
				continue
			}
			headers := map[string]bool{}
			rowData := map[string]Cell{}
			ref := ""
			headers["row_id"] = true
			rowData["row_id"] = Cell{i + 1}
			for _, header := range sheet.Headers {
				if headers[header] {
					return fmt.Errorf("duplicate header: %s", header)
				}
				headers[header] = true
				rowData[header] = Cell{nil}
			}
			for columnIndex, cellValue := range row {
				if hasRef && columnIndex == refColumn {
					ref = cellValue.(string)
				}
				if columnIndex >= len(sheet.Headers) {
					continue
				}
				rowData[sheet.Headers[columnIndex]] = Cell{cellValue}
			}
			sheet.Data = append(sheet.Data, rowData)
			if hasRef {
				sheet.DataByRef[ref] = rowData
			}
		}
	}
	return nil
}

func (s *Service) ParseGlobal() error {
	s.Global = &Global{
		Preview:                  s.Sheets["global"].DataByRef["preview"]["value"].Bool(),
		Production:               s.Sheets["global"].DataByRef["production"]["value"].Bool(),
		Thumbnails:               s.Sheets["global"].DataByRef["thumbnails"]["value"].Bool(),
		Titles:                   s.Sheets["global"].DataByRef["titles"]["value"].Bool(),
		PreviewThumbnailsFolder:  s.Sheets["global"].DataByRef["preview_thumbnails_folder"]["value"].String(),
		PreviewThumbnailsDropbox: s.Sheets["global"].DataByRef["preview_thumbnails_dropbox"]["value"].String(),
		//	Data:
	}
	data := map[string]Cell{}
	for name, row := range s.Sheets["global"].DataByRef {
		data[name] = row["value"]
	}
	s.Global.Data = data

	return nil
}

func (item *Item) Set(s *Service, column string, value any, force bool) error {
	if !force && !item.Data[column].Empty() {
		return fmt.Errorf("cell %v is not empty, value = %#v (%v)", column, item.Data[column].Value, item.String())
	}
	if err := item.Expedition.ItemSheet.Set(s.SheetsService, item.RowId, column, value, force); err != nil {
		return fmt.Errorf("unable to update cell %v (%v): %w", column, item.String(), err)
	}
	item.Data[column] = Cell{value}
	return nil
}

// columnIDToLetter converts a column ID to a column letter (e.g., 1 -> A, 27 -> AA).
func columnIDToLetter(columnID int) string {
	var columnLetter strings.Builder
	for columnID > 0 {
		columnID-- // Adjust columnID to be 0-indexed
		columnLetter.WriteByte(byte('A' + columnID%26))
		columnID /= 26
	}
	// Reverse the string as we constructed it backwards
	return reverseString(columnLetter.String())
}

// reverseString reverses a string.
func reverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

// getCellRange returns the cell range from a column ID and row ID.
func getCellRange(columnID, rowID int) string {
	columnLetter := columnIDToLetter(columnID)
	return fmt.Sprintf("%s%d", columnLetter, rowID)
}

func (s *Service) WriteVideosPreview() error {

	if !s.Global.Preview {
		return nil
	}

	// write preview data
	// expedition	type	key	changed	video_privacy_status	video_publish_at	video_title	video_description
	headers := []string{"video_privacy_status", "video_publish_at", "video_title", "video_description", "video_tags"}
	var values [][]any

	var keys []*Item
	for key := range s.VideoPreviewData {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Expedition != keys[j].Expedition {
			return keys[i].Expedition.RowId < keys[j].Expedition.RowId
		}
		return keys[i].RowId < keys[j].RowId
	})

	for _, item := range keys {
		data := s.VideoPreviewData[item]
		var value []any
		value = append(value, item.Expedition.Ref)
		value = append(value, item.Type)
		value = append(value, item.Key)
		for _, name := range headers {
			value = append(value, data[name])
		}
		values = append(values, value)
	}

	// Define the range to append the data
	rangeToAppend := fmt.Sprintf("%s!A1", "preview_videos")

	// Create a request to append the specified values
	valueRange := &sheets.ValueRange{
		Values: values,
	}

	// Execute the append request
	_, err := s.SheetsService.Spreadsheets.Values.Append(SPREADSHEET_ID, rangeToAppend, valueRange).
		ValueInputOption("RAW").
		InsertDataOption("INSERT_ROWS").
		Do()
	if err != nil {
		return fmt.Errorf("unable to append rows to preview_videos sheet: %w", err)
	}

	return nil
}

func (s *Service) WritePlaylistsPreview() error {

	if !s.Global.Preview {
		return nil
	}

	_, err := s.SheetsService.Spreadsheets.Values.Clear(
		SPREADSHEET_ID,
		fmt.Sprintf("%s!2:1000", "preview_playlists"),
		&sheets.ClearValuesRequest{},
	).Do()
	if err != nil {
		return fmt.Errorf("unable to clear preview_playlists sheet data: %w", err)
	}

	// write preview data
	headers := []string{"playlist_title", "playlist_description", "playlist_content"}
	var values [][]any

	var keys []HasPlaylist
	for key := range s.PlaylistPreviewData {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].GetExpedition() != keys[j].GetExpedition() {
			return keys[i].GetExpedition().RowId < keys[j].GetExpedition().RowId
		}
		if _, ok := keys[i].(*Expedition); ok {
			return true
		}
		if _, ok := keys[j].(*Expedition); ok {
			return false
		}
		s1 := keys[i].(*Section)
		s2 := keys[j].(*Section)
		return s1.RowId < s2.RowId
	})

	for _, item := range keys {
		data := s.PlaylistPreviewData[item]
		var value []any
		expeditionRef := item.GetExpedition().Ref
		var sectionRef any
		if section, ok := item.(*Section); ok {
			sectionRef = section.Ref
		}
		value = append(value, expeditionRef)
		value = append(value, sectionRef)
		for _, name := range headers {
			value = append(value, data[name])
		}
		values = append(values, value)
	}

	// Define the range to append the data
	rangeToAppend := fmt.Sprintf("%s!A1", "preview_playlists")

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
		return fmt.Errorf("unable to append rows to preview_playlists sheet: %w", err)
	}

	return nil
}

func (s *Service) ParseExpeditions() error {
	for _, data := range s.Sheets["expedition"].Data {
		ref := data["ref"].String()

		sheetId, err := getSpreadsheetIDFromLink(data["data_sheet"].String())
		if err != nil {
			return fmt.Errorf("unable to get sheet id: %w", err)
		}

		s.Expeditions[ref] = &Expedition{
			RowId:              data["row_id"].Int(),
			Ref:                ref,
			Name:               data["name"].String(),
			Process:            data["process"].Bool(),
			VideosFolder:       data["videos_folder"].String(),
			ThumbnailsFolder:   data["thumbnails_folder"].String(),
			VideosDropbox:      data["videos_dropbox"].String(),
			ThumbnailsDropbox:  data["thumbnails_dropbox"].String(),
			ExpeditionPlaylist: data["expedition_playlist"].Bool(),
			SectionPlaylists:   data["section_playlists"].Bool(),
			DataSheetId:        sheetId,
			PlaylistId:         data["playlist_id"].String(),
			Data:               data,
			Sheets:             map[string]*Sheet{},
			SectionsByRef:      map[string]*Section{},
			Templates:          template.New("").Funcs(Funcs),
		}
	}
	return nil
}

func getSpreadsheetIDFromLink(link string) (string, error) {
	if link == "" {
		return "", errors.New("link is empty")
	}

	// Example link: https://docs.google.com/spreadsheets/d/<spreadsheet_id>/edit
	parts := strings.Split(link, "/")
	for i, part := range parts {
		if part == "d" && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}

	return "", errors.New("invalid Google Sheets link")
}

func (s *Service) ParseSections() error {
	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		sheet, ok := expedition.Sheets["section"]
		if !ok {
			continue
		}

		for _, data := range sheet.Data {
			ref := data["ref"].String()
			expedition.SectionsByRef[ref] = &Section{
				RowId:      data["row_id"].Int(),
				Ref:        ref,
				Name:       data["name"].String(),
				PlaylistId: data["playlist_id"].String(),
				Data:       data,
				Expedition: expedition,
			}
			expedition.Sections = append(expedition.Sections, expedition.SectionsByRef[ref])
		}
	}
	return nil
}

func (s *Service) ParseItems() error {
	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}

		for _, data := range expedition.ItemSheet.Data {

			parseLocation := func(s string) Location {
				if data[s+"_name"].Empty() {
					return Location{}
				}
				elevation, ok := data[s+"_elevation"]
				if ok {
					return Location{Name: data[s+"_name"].String(), Elevation: elevation.Int()}
				} else {
					return Location{Name: data[s+"_name"].String()}
				}
			}
			var via []Location
			viaId := 1
			for {
				v := parseLocation(fmt.Sprintf("via%d", viaId))
				if v.Name == "" {
					break
				}
				via = append(via, v)
				viaId++
			}
			var section *Section
			var sectionRef string
			if data["section_ref"].String() != "" {
				sectionRef = data["section_ref"].String()
				var ok bool
				section, ok = expedition.SectionsByRef[sectionRef]
				if !ok {
					return fmt.Errorf("section not found (%v): %s", expedition.Ref, sectionRef)
				}
			}

			var release time.Time
			if !data["release"].Empty() {
				release = data["release"].Time()
			}

			item := &Item{
				RowId:             data["row_id"].Int(),
				Expedition:        expedition,
				Type:              data["type"].String(),
				Key:               data["key"].Int(),
				Video:             data["video"].Bool(),
				YoutubeId:         data["youtube_id"].String(),
				Ready:             data["ready"].Bool(),
				DoThumbnail:       data["do_thumbnail"].Bool(),
				YoutubeTranscript: data["transcript"].String(),
				Tags:              strings.Split(data["tags"].String(), "\n"),
				Release:           release,
				Data:              data,
				Template:          data["template"].String(),
				From:              parseLocation("from"),
				To:                parseLocation("to"),
				Via:               via,
				Section:           section,
				SectionRef:        sectionRef,
			}
			expedition.Items = append(expedition.Items, item)
			if section != nil {
				section.Items = append(section.Items, item)
			}
		}
	}
	return nil
}

func (s *Service) ParseTemplates() error {
	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		sheet, ok := expedition.Sheets["template"]
		if !ok {
			continue
		}

		for _, data := range sheet.Data {
			if data["template"].Empty() {
				continue
			}
			ref := data["ref"].String()
			_, err := expedition.Templates.New(ref).Parse(data["template"].String())
			if err != nil {
				return fmt.Errorf("error parsing template (%v): %w", ref, err)
			}
		}

		for _, data := range s.Sheets["template"].Data {
			if data["template"].Empty() {
				continue
			}
			ref := data["ref"].String()
			_, err := expedition.Templates.New(ref).Parse(data["template"].String())
			if err != nil {
				return fmt.Errorf("error parsing template (%v): %w", ref, err)
			}
		}
	}
	return nil
}

func (s *Service) ParseLinkedData() error {
	for _, expedition := range s.Expeditions {
		for _, sheet := range expedition.Sheets {
			for _, header := range sheet.Headers {
				if strings.HasSuffix(header, "_ref") {
					linkedSheetName := strings.TrimSuffix(header, "_ref")

					// first check if the expedition specific linked sheet exists
					linkedSheet, ok := expedition.Sheets[linkedSheetName]
					if !ok {
						// if not, check if a general linked sheet exists
						linkedSheet, ok = s.Sheets[linkedSheetName]
						if !ok {
							return fmt.Errorf("linked sheet not found: %s", linkedSheetName)
						}
					}

					for i, data := range sheet.Data {
						if data[header].Empty() {
							continue
						}
						ref := data[header].String()
						linkedData := linkedSheet.DataByRef[ref]
						if linkedData == nil {
							return fmt.Errorf("linked data not found: %s", ref)
						}
						sheet.Data[i][linkedSheetName] = Cell{linkedData}
					}
				}
			}
		}
	}
	return nil
}

func (s *Service) UpdateVideoTitles() error {
	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		for _, item := range expedition.Items {
			if !item.Video {
				continue
			}
			f := func(templateName, columnName string) error {
				if expedition.Templates.Lookup(templateName) == nil {
					return nil
				}
				buf := &strings.Builder{}
				if err := expedition.Templates.ExecuteTemplate(buf, templateName, item); err != nil {
					return fmt.Errorf("unable to execute template (%v): %w", templateName, err)
				}
				value := buf.String()
				if item.Data[columnName].String() == value {
					return nil
				}
				if err := item.Set(s, columnName, value, true); err != nil {
					return fmt.Errorf("unable to update column %v (%v): %w", columnName, item.String(), err)
				}
				return nil
			}
			if err := f("video_title_1", "video_title_1"); err != nil {
				return fmt.Errorf("running video_title_1 template: %w", err)
			}
			if err := f("video_title_2", "video_title_2"); err != nil {
				return fmt.Errorf("running video_title_2 template: %w", err)
			}
		}
	}
	return nil
}

func (s *Service) StorePlaylistPreviewOps(parent HasPlaylist, name string, ops []string) {
	if _, ok := s.PlaylistPreviewData[parent]; !ok {
		s.PlaylistPreviewData[parent] = map[string]any{}
	}
	if len(ops) == 0 {
		s.PlaylistPreviewData[parent][name] = "=== UNCHANGED ==="
	} else {
		s.PlaylistPreviewData[parent][name] = fmt.Sprintf("=== CHANGED ===\n%s", strings.Join(ops, "\n"))
	}
}

func (s *Service) StorePlaylistPreviewDeleted(parent HasPlaylist) {
	if _, ok := s.PlaylistPreviewData[parent]; !ok {
		s.PlaylistPreviewData[parent] = map[string]any{}
	}
	s.PlaylistPreviewData[parent]["playlist_title"] = fmt.Sprintf("=== DELETED ===")
	s.PlaylistPreviewData[parent]["playlist_description"] = fmt.Sprintf("=== DELETED ===")
	s.PlaylistPreviewData[parent]["playlist_content"] = fmt.Sprintf("=== DELETED ===")
}

func (s *Service) StorePlaylistPreview(parent HasPlaylist, name, before, after string) {
	if _, ok := s.PlaylistPreviewData[parent]; !ok {
		s.PlaylistPreviewData[parent] = map[string]any{}
	}
	if before == "" && after == "" {
		s.PlaylistPreviewData[parent][name] = fmt.Sprintf("=== EMPTY ===")
	} else if before == "" {
		s.PlaylistPreviewData[parent][name] = fmt.Sprintf(
			"=== NEW ===\n%s",
			after,
		)
	} else if before == after {
		s.PlaylistPreviewData[parent][name] = fmt.Sprintf(
			"=== UNCHANGED ===\n%s",
			after,
		)
	} else {
		s.PlaylistPreviewData[parent][name] = fmt.Sprintf(
			"=== CHANGED ===\n%s\n=== BEFORE ===\n%s\n=== DIFF ===\n%s",
			after,
			before,
			textdiff.Unified("before", "after", before, after),
		)
	}
}

func (s *Service) StoreVideoPreview(item *Item, name, before, after string) {
	if _, ok := s.VideoPreviewData[item]; !ok {
		s.VideoPreviewData[item] = map[string]any{}
	}
	if before == "" && after == "" {
		s.VideoPreviewData[item][name] = fmt.Sprintf("=== EMPTY ===")
	} else if before == "" {
		s.VideoPreviewData[item][name] = fmt.Sprintf(
			"=== NEW ===\n%s",
			after,
		)
	} else if before == after {
		s.VideoPreviewData[item][name] = fmt.Sprintf(
			"=== UNCHANGED ===\n%s",
			after,
		)
	} else {
		s.VideoPreviewData[item][name] = fmt.Sprintf(
			"=== CHANGED ===\n%s\n=== BEFORE ===\n%s\n=== DIFF ===\n%s",
			after,
			before,
			textdiff.Unified("before", "after", before, after),
		)
	}
}
