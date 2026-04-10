package history

import "strings"

type Event struct {
	Title  string
	Detail string
}

type Log struct {
	Events []Event
}

func (l *Log) Add(title, detail string) {
	l.Events = append(l.Events, Event{Title: title, Detail: detail})
}

func (l Log) Markdown() string {
	lines := []string{"# Session History", ""}
	for _, event := range l.Events {
		lines = append(lines, "- "+event.Title+": "+event.Detail)
	}
	return strings.Join(lines, "\n")
}
