package graphs

import (
	"strconv"
	"strings"

	"ascaris/internal/commands"
	"ascaris/internal/models"
)

type CommandGraph struct {
	Builtins   []models.PortingModule
	PluginLike []models.PortingModule
	SkillLike  []models.PortingModule
}

func (g CommandGraph) Markdown() string {
	lines := []string{
		"# Command Graph",
		"",
		"Builtins: " + strconv.Itoa(len(g.Builtins)),
		"Plugin-like commands: " + strconv.Itoa(len(g.PluginLike)),
		"Skill-like commands: " + strconv.Itoa(len(g.SkillLike)),
	}
	return strings.Join(lines, "\n")
}

func BuildCommandGraph() CommandGraph {
	all := commands.List(true, true)
	graph := CommandGraph{}
	for _, module := range all {
		source := strings.ToLower(module.SourceHint)
		switch {
		case strings.Contains(source, "plugin"):
			graph.PluginLike = append(graph.PluginLike, module)
		case strings.Contains(source, "skills"):
			graph.SkillLike = append(graph.SkillLike, module)
		default:
			graph.Builtins = append(graph.Builtins, module)
		}
	}
	return graph
}
