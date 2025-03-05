package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"text/template"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/youtube/v3"
)

type Expedition struct {
	Ref              string
	Name             string
	Ready            bool
	Process          bool
	Thumbnails       bool
	VideosFolder     string
	ThumbnailsFolder string
	Data             map[string]any
	Sections         map[string]*Section
	SectionsOrdered  []*Section
	Items            []*Item
	Templates        *template.Template
}

type Section struct {
	Expedition *Expedition
	Items      []*Item
	Ref        string
	Name       string
	Data       map[string]any
}

type Item struct {
	Type          string
	Key           int
	Video         bool
	Template      string
	Ready         bool
	Release       time.Time
	From, To      Location
	Via           []Location
	Section       *Section
	Expedition    *Expedition
	Data          map[string]any
	VideoFile     *drive.File
	ThumbnailFile *drive.File
	YoutubeId     string
	YoutubeVideo  *youtube.Video
}

func (item Item) Metadata() (string, error) {
	metaData := Meta{
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

func (item Item) String() string {
	section := ""
	if item.Section != nil {
		section = item.Section.Ref + " "
	}
	return fmt.Sprintf("[%s %s %s%d]", item.Expedition.Ref, item.Type, section, item.Key)
}

type Location struct {
	Name      string
	Elevation int
}

type Sheet struct {
	Name       string
	Expedition *Expedition
	Headers    []string
	Data       []map[string]interface{}
	DataByRef  map[string]map[string]interface{}
}

func (s *Sheet) FullName() string {
	if s.Expedition != nil {
		return s.Expedition.Ref + " - " + s.Name
	} else {
		return s.Name
	}
}
