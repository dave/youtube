package main

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
