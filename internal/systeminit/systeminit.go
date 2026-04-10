package systeminit

import (
	"strconv"
	"strings"

	"ascaris/internal/commands"
	"ascaris/internal/setup"
	"ascaris/internal/tools"
)

func Build(root string, trusted bool) string {
	report := setup.Run(root, trusted)
	commandEntries, _ := commands.ListAtRoot(root, true, true)
	toolEntries, _ := tools.ListAtRoot(root, false, true, nil)
	lines := []string{
		"# System Init",
		"",
		"Trusted: " + strconv.FormatBool(report.Trusted),
		"Built-in command names: " + strconv.Itoa(len(commands.BuiltInNames())),
		"Loaded command entries: " + strconv.Itoa(len(commandEntries)),
		"Loaded tool entries: " + strconv.Itoa(len(toolEntries)),
		"",
		"Startup steps:",
	}
	for _, step := range report.Setup.StartupSteps() {
		lines = append(lines, "- "+step)
	}
	return strings.Join(lines, "\n")
}
