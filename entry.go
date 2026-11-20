package main

import (
	"strings"
	"time"
)

const headerLayout = "2006/01/02 15:04"

func header(now time.Time) string {
	return now.Format(headerLayout) + ".."
}

// newEntry returns the entry text ready to append, or "" if body is blank.
func newEntry(now time.Time, body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.Trim(body, " \t\r\n")
	if body == "" {
		return ""
	}
	return header(now) + " " + body
}
