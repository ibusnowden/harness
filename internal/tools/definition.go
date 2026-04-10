package tools

type Definition struct {
	Name    string
	Purpose string
}

var DefaultDefinitions = []Definition{
	{Name: "port_manifest", Purpose: "Summarize the active Go workspace"},
	{Name: "query_engine", Purpose: "Render a Go-first harness summary"},
}
