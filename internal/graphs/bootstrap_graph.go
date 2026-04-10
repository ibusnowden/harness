package graphs

import "strings"

type BootstrapGraph struct {
	Stages []string
}

func (g BootstrapGraph) Markdown() string {
	lines := []string{"# Bootstrap Graph", ""}
	for _, stage := range g.Stages {
		lines = append(lines, "- "+stage)
	}
	return strings.Join(lines, "\n")
}

func BuildBootstrapGraph() BootstrapGraph {
	return BootstrapGraph{
		Stages: []string{
			"top-level prefetch side effects",
			"warning handler and environment guards",
			"CLI parser and pre-action trust gate",
			"setup() + commands/agents parallel load",
			"deferred init after trust",
			"mode routing: local / remote / ssh / teleport / direct-connect / deep-link",
			"query engine submit loop",
		},
	}
}
