package upload

import (
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

func (s *Service) ClearPreviewSheet() error {
	if !s.Global.Preview {
		return nil
	}

	fmt.Println("Clearing preview sheet")

	// clear "preview_videos" sheet, but leave first row (headers)
	_, err := s.SheetsService.Spreadsheets.Values.Clear(
		SPREADSHEET_ID,
		fmt.Sprintf("%s!2:1000", "preview_videos"),
		&sheets.ClearValuesRequest{},
	).Do()
	if err != nil {
		return fmt.Errorf("unable to clear preview_videos sheet data: %w", err)
	}
	return nil
}

func (s *Service) GetAllSheetsData() error {
	for _, sheetData := range s.Spreadsheet.Sheets {
		skip := map[string]bool{
			"global":            true,
			"expedition":        true,
			"preview_videos":    true,
			"preview_playlists": true,
		}
		if skip[sheetData.Properties.Title] {
			continue
		}
		if err := s.GetSheetData(sheetData.Properties.Title); err != nil {
			return fmt.Errorf("unable to get sheet data: %w", err)
		}
	}
	return nil
}

func (s *Service) GetSheetData(titles ...string) error {
	for _, title := range titles {
		sheet := &Sheet{
			DataByRef: map[string]map[string]interface{}{},
		}
		s.Sheets[title] = sheet

		for ref := range s.Expeditions {
			if strings.HasPrefix(title, ref+"_") {
				sheet.Name = strings.TrimPrefix(title, ref+"_")
				sheet.Expedition = s.Expeditions[ref]
			}
		}

		if sheet.Expedition != nil && !sheet.Expedition.Process {
			return nil
		}

		fmt.Println("Getting raw data for sheet", title)
		values, err := s.SheetsService.Spreadsheets.Values.
			Get(SPREADSHEET_ID, title).
			ValueRenderOption("UNFORMATTED_VALUE").
			Do()
		if err != nil {
			return fmt.Errorf("unable to retrieve values from sheet: %w", err)
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
			rowData := map[string]any{}
			ref := ""
			rowData["row_id"] = i + 1
			for columnIndex, cellValue := range row {
				if hasRef && columnIndex == refColumn {
					ref = cellValue.(string)
				}
				rowData[sheet.Headers[columnIndex]] = cellValue
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
		Preview:                 s.Sheets["global"].DataByRef["preview"]["value"].(bool),
		Production:              s.Sheets["global"].DataByRef["production"]["value"].(bool),
		PreviewThumbnailsFolder: s.Sheets["global"].DataByRef["preview_thumbnails_folder"]["value"].(string),
	}

	return nil
}

func (s *Service) WriteVideosPreview() error {

	if !s.Global.Preview {
		return nil
	}

	// write preview data
	// expedition	type	key	changed	video_privacy_status	video_publish_at	video_title	video_description	thumbnail_top	thumbnail_bottom
	headers := []string{"video_privacy_status", "video_publish_at", "video_title", "video_description", "thumbnail_top", "thumbnail_bottom"}
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
		ref := data["ref"].(string)
		s.Expeditions[ref] = &Expedition{
			RowId:              data["row_id"].(int),
			Ref:                ref,
			Name:               data["name"].(string),
			Ready:              data["ready"].(bool),
			Process:            data["process"].(bool),
			Thumbnails:         data["thumbnails"].(bool),
			VideosFolder:       stringify(data["videos_folder"]),
			ThumbnailsFolder:   stringify(data["thumbnails_folder"]),
			ExpeditionPlaylist: data["expedition_playlist"].(bool),
			SectionPlaylists:   data["section_playlists"].(bool),
			Data:               data,
			SectionsByRef:      map[string]*Section{},
			Templates:          template.New("").Funcs(Funcs),
		}
	}
	return nil
}

func (s *Service) ParseSections() error {
	for _, expedition := range s.Expeditions {
		if !expedition.Process {
			continue
		}
		sheet, ok := s.Sheets[expedition.Ref+"_section"]
		if !ok {
			continue
		}

		for _, data := range sheet.Data {
			ref := data["ref"].(string)
			expedition.SectionsByRef[ref] = &Section{
				RowId:      data["row_id"].(int),
				Ref:        ref,
				Name:       stringify(data["name"]),
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
		sheet, ok := s.Sheets[expedition.Ref+"_item"]
		if !ok {
			continue
		}

		for _, data := range sheet.Data {

			parseLocation := func(s string) Location {
				if empty(data[s+"_name"]) {
					return Location{}
				}
				return Location{Name: data[s+"_name"].(string), Elevation: int(data[s+"_elevation"].(float64))}
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
			if data["section_ref"] != nil && data["section_ref"].(string) != "" {
				sectionRef = data["section_ref"].(string)
				var ok bool
				section, ok = expedition.SectionsByRef[sectionRef]
				if !ok {
					return fmt.Errorf("section not found: %s", sectionRef)
				}
			}

			var release time.Time
			if !empty(data["release"]) {
				release = floatToTime(data["release"].(float64))
			}

			item := &Item{
				RowId:      data["row_id"].(int),
				Expedition: expedition,
				Type:       stringify(data["type"]),
				Key:        int(data["key"].(float64)),
				Video:      data["video"].(bool),
				Ready:      data["ready"].(bool),
				Release:    release,
				Data:       data,
				Template:   stringify(data["template"]),
				From:       parseLocation("from"),
				To:         parseLocation("to"),
				Via:        via,
				Section:    section,
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
		sheet, ok := s.Sheets[expedition.Ref+"_template"]
		if !ok {
			continue
		}

		for _, data := range sheet.Data {
			if empty(data["template"]) {
				continue
			}
			ref := data["ref"].(string)
			_, err := expedition.Templates.New(ref).Parse(data["template"].(string))
			if err != nil {
				return fmt.Errorf("error parsing template: %w", err)
			}
		}

		for _, data := range s.Sheets["template"].Data {
			if empty(data["template"]) {
				continue
			}
			ref := data["ref"].(string)
			_, err := expedition.Templates.New(ref).Parse(data["template"].(string))
			if err != nil {
				return fmt.Errorf("error parsing template: %w", err)
			}
		}
	}
	return nil
}

func (s *Service) ParseLinkedData() error {
	for _, sheet := range s.Sheets {
		for _, header := range sheet.Headers {
			if strings.HasSuffix(header, "_ref") {
				linkedSheetName := strings.TrimSuffix(header, "_ref")

				// first check if the expedition specific linked sheet exists
				linkedSheet, ok := s.Sheets[sheet.Expedition.Ref+"_"+linkedSheetName]
				if !ok {
					// if not, check if a general linked sheet exists
					linkedSheet, ok = s.Sheets[linkedSheetName]
					if !ok {
						return fmt.Errorf("linked sheet not found: %s", linkedSheetName)
					}
				}

				for i, data := range sheet.Data {
					if empty(data[header]) {
						continue
					}
					ref := data[header].(string)
					linkedData := linkedSheet.DataByRef[ref]
					if linkedData == nil {
						return fmt.Errorf("linked data not found: %s", ref)
					}
					sheet.Data[i][linkedSheetName] = linkedData
				}
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

func floatToTime(f float64) time.Time {
	// Google Sheets base date is December 30, 1899
	baseDate := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	// Add the number of days (including fractional days) to the base date
	return baseDate.Add(time.Duration(f * 24 * float64(time.Hour)))
}

func empty(v any) bool {
	if v == nil {
		return true
	}
	switch v := v.(type) {
	case string:
		return v == ""
	}
	return false
}

func stringify(v any) string {
	if v == nil {
		return ""
	}
	return v.(string)
}
