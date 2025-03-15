package uploader

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"text/template"
	"time"
)

var Funcs = template.FuncMap{

	"empty": func(v any) bool {
		if v == nil {
			return true
		}
		switch v := v.(type) {
		case string:
			return v == ""
		}
		return false
	},

	"title": func(s string) string {
		// Strings ending with 's (e.g. Tillman's pass) needs special case: 'S -> 's
		return strings.Replace(strings.Title(strings.ToLower(s)), "'S", "'s", -1)
	},
	"upper": func(s string) string { return strings.ToUpper(s) },
	"lower": func(s string) string { return strings.ToLower(s) },
	"int": func(a any) int {
		switch v := a.(type) {
		case float64:
			return int(v)
		case int:
			return v
		default:
			return 0
		}
	},

	"commas": func(v int) string {
		sign := ""

		// Min int64 can't be negated to a usable value, so it has to be special cased.
		if v == math.MinInt64 {
			return "-9,223,372,036,854,775,808"
		}

		if v < 0 {
			sign = "-"
			v = 0 - v
		}

		parts := []string{"", "", "", "", "", "", ""}
		j := len(parts) - 1

		for v > 999 {
			parts[j] = strconv.FormatInt(int64(v%1000), 10)
			switch len(parts[j]) {
			case 2:
				parts[j] = "0" + parts[j]
			case 1:
				parts[j] = "00" + parts[j]
			}
			v = v / 1000
			j--
		}
		parts[j] = strconv.Itoa(int(v))
		return sign + strings.Join(parts[j:], ",")
	},

	"add": func(a, b int) int { return a + b },
	"sub": func(a, b int) int { return a - b },

	// date formats a date as "May 20th"
	"date": func(date float64) string {
		// Google Sheets base date is December 30, 1899
		baseDate := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		// Add the number of days (including fractional days) to the base date
		t := baseDate.Add(time.Duration(date * 24 * float64(time.Hour)))

		day := t.Day()
		suffix := "th"
		switch day % 10 {
		case 1:
			if day != 11 {
				suffix = "st"
			}
		case 2:
			if day != 12 {
				suffix = "nd"
			}
		case 3:
			if day != 13 {
				suffix = "rd"
			}
		}
		return fmt.Sprintf("%s %d%s", t.Format("January"), day, suffix)
	},

	"dict": func(values ...interface{}) (map[string]interface{}, error) {
		if len(values)%2 != 0 {
			return nil, errors.New("invalid dict call")
		}
		dict := make(map[string]interface{}, len(values)/2)
		for i := 0; i < len(values); i += 2 {
			key, ok := values[i].(string)
			if !ok {
				return nil, errors.New("dict keys must be strings")
			}
			dict[key] = values[i+1]
		}
		return dict, nil
	},
}
