package execution

import (
	"strings"

	"ascaris/internal/commands"
	"ascaris/internal/tools"
)

type MirroredCommand struct {
	Name       string
	SourceHint string
}

func (c MirroredCommand) Execute(prompt string) string {
	return commands.Execute(c.Name, prompt).Message
}

type MirroredTool struct {
	Name       string
	SourceHint string
}

func (t MirroredTool) Execute(payload string) string {
	return tools.Execute(t.Name, payload).Message
}

type Registry struct {
	Commands []MirroredCommand
	Tools    []MirroredTool
}

func Build(root string) (Registry, error) {
	commandEntries, err := commands.Catalog(root)
	if err != nil {
		return Registry{}, err
	}
	toolEntries, err := tools.Catalog(root, tools.CatalogOptions{IncludeMCP: true})
	if err != nil {
		return Registry{}, err
	}
	registry := Registry{
		Commands: make([]MirroredCommand, 0, len(commandEntries)),
		Tools:    make([]MirroredTool, 0, len(toolEntries)),
	}
	for _, entry := range commandEntries {
		registry.Commands = append(registry.Commands, MirroredCommand{Name: entry.Name, SourceHint: entry.SourceHint})
	}
	for _, entry := range toolEntries {
		registry.Tools = append(registry.Tools, MirroredTool{Name: entry.Name, SourceHint: entry.SourceHint})
	}
	return registry, nil
}

func (r Registry) Command(name string) *MirroredCommand {
	needle := strings.ToLower(name)
	for _, command := range r.Commands {
		if strings.ToLower(command.Name) == needle {
			copy := command
			return &copy
		}
	}
	return nil
}

func (r Registry) Tool(name string) *MirroredTool {
	needle := strings.ToLower(name)
	for _, tool := range r.Tools {
		if strings.ToLower(tool.Name) == needle {
			copy := tool
			return &copy
		}
	}
	return nil
}
