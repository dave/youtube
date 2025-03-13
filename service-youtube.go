package main

import "regexp"

var ApiPartsRead = []string{"snippet", "localizations", "status", "fileDetails"}
var ApiPartsInsert = []string{"snippet", "localizations", "status"}
var ApiPartsUpdate = []string{"snippet", "localizations", "status"}

var MetaRegex = regexp.MustCompile(`\n{(.*)}$`)
