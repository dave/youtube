package main

import (
	"fmt"
	"strings"
	"text/template"
	"time"

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

func (s *Service) GetAllSheetsData() error {

	for _, sheetData := range s.Spreadsheet.Sheets {
		if sheetData.Properties.Title == "expedition" {
			// already done expedition sheet
			continue
		}
		if err := s.GetSheetData(sheetData.Properties.Title); err != nil {
			return fmt.Errorf("unable to get sheet data: %w", err)
		}
	}
	return nil
}

func (s *Service) GetSheetData(title string) error {
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
		rowData := map[string]interface{}{}
		ref := ""
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
	return nil
}

func (s *Service) ParseExpeditions() error {
	for _, data := range s.Sheets["expedition"].Data {
		ref := data["ref"].(string)
		s.Expeditions[ref] = &Expedition{
			Ref:              ref,
			Name:             data["name"].(string),
			Ready:            data["ready"].(bool),
			Process:          data["process"].(bool),
			Thumbnails:       data["thumbnails"].(bool),
			VideosFolder:     stringify(data["videos_folder"]),
			ThumbnailsFolder: stringify(data["thumbnails_folder"]),
			Data:             data,
			Sections:         map[string]*Section{},
			Templates:        template.New("").Funcs(Funcs),
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
			expedition.Sections[ref] = &Section{
				Ref:        ref,
				Name:       stringify(data["name"]),
				Data:       data,
				Expedition: expedition,
			}
			expedition.SectionsOrdered = append(expedition.SectionsOrdered, expedition.Sections[ref])
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
				section, ok = expedition.Sections[sectionRef]
				if !ok {
					return fmt.Errorf("section not found: %s", sectionRef)
				}
			}

			var release time.Time
			if !empty(data["release"]) {
				release = floatToTime(data["release"].(float64))
			}

			item := &Item{
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

func floatToTime(f float64) time.Time {
	// Google Sheets base date is December 30, 1899
	baseDate := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	// Add the number of days (including fractional days) to the base date
	return baseDate.Add(time.Duration(f * 24 * float64(time.Hour)))
}
