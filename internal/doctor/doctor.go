package doctor

import (
	"encoding/json"
	"strconv"
	"strings"

	"ascaris/internal/commands"
	"ascaris/internal/config"
	"ascaris/internal/sessions"
	"ascaris/internal/setup"
	"ascaris/internal/tools"
)

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

type Report struct {
	Message string  `json:"message"`
	Checks  []Check `json:"checks"`
}

func Build(root string) Report {
	setupReport := setup.Run(root, true)
	runtimeConfig, configErr := config.Load(root)
	checks := []Check{
		{Name: "go_toolchain", Status: "ok", Summary: "detected " + setupReport.Setup.GoVersion},
		{Name: "config_home", Status: "ok", Summary: "using " + sessions.ConfigHome(root)},
	}
	if configErr != nil {
		checks = append(checks, Check{Name: "config", Status: "error", Summary: configErr.Error()})
	} else {
		checks = append(checks, Check{
			Name:    "config",
			Status:  "ok",
			Summary: "loaded " + strconv.Itoa(len(runtimeConfig.LoadedEntries())) + " config files",
		})
		commandEntries, err := commands.Catalog(root)
		if err != nil {
			checks = append(checks, Check{Name: "command_registry", Status: "error", Summary: err.Error()})
		} else {
			checks = append(checks, Check{Name: "command_registry", Status: "ok", Summary: "loaded " + strconv.Itoa(len(commandEntries)) + " live commands"})
		}
		toolEntries, err := tools.Catalog(root, tools.CatalogOptions{IncludeMCP: true})
		if err != nil {
			checks = append(checks, Check{Name: "tool_registry", Status: "error", Summary: err.Error()})
		} else {
			checks = append(checks, Check{Name: "tool_registry", Status: "ok", Summary: "loaded " + strconv.Itoa(len(toolEntries)) + " live tools"})
		}
		if runtimeConfig.OAuth() == nil {
			checks = append(checks, Check{Name: "oauth", Status: "warn", Summary: "oauth is not configured"})
		} else {
			checks = append(checks, Check{Name: "oauth", Status: "ok", Summary: "oauth settings are configured"})
		}
	}
	return Report{
		Message: "local-only health report",
		Checks:  checks,
	}
}

func (r Report) Text() string {
	lines := []string{
		"Ascaris Doctor",
		"Summary: " + r.Message,
		"",
		"Checks:",
	}
	for _, check := range r.Checks {
		lines = append(lines, "- "+check.Name+" ["+check.Status+"] "+check.Summary)
	}
	return strings.Join(lines, "\n")
}

func (r Report) JSON() string {
	data, _ := json.MarshalIndent(r, "", "  ")
	return string(data)
}
