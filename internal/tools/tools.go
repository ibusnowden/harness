package tools

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"ascaris/internal/config"
	"ascaris/internal/mcp"
	"ascaris/internal/models"
	"ascaris/internal/permissions"
	"ascaris/internal/plugins"
)

type Execution struct {
	Name       string
	SourceHint string
	Payload    string
	Handled    bool
	Message    string
}

type CatalogOptions struct {
	SimpleMode        bool
	IncludeMCP        bool
	PermissionContext *permissions.ToolPermissionContext
}

func MustModules() []models.PortingModule {
	return builtInModules()
}

func Catalog(root string, options CatalogOptions) ([]models.PortingModule, error) {
	modules := filterModules(builtInModules(), options)
	runtimeConfig, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	manager := plugins.NewManager(root, runtimeConfig)
	pluginTools, err := manager.AggregatedTools()
	if err != nil {
		return nil, err
	}
	for _, tool := range pluginTools {
		modules = append(modules, models.PortingModule{
			Name:           tool.Name,
			Responsibility: tool.Description,
			SourceHint:     "plugin/" + tool.PluginID,
			Status:         "live",
		})
	}
	if options.IncludeMCP {
		registry := mcp.FromConfig(runtimeConfig)
		if err := registry.Discover(); err != nil {
			return nil, err
		}
		for _, state := range registry.States() {
			for _, tool := range state.Tools {
				modules = append(modules, models.PortingModule{
					Name:           tool.Qualified,
					Responsibility: tool.Description,
					SourceHint:     "mcp/" + tool.ServerName,
					Status:         string(state.Status),
				})
			}
		}
	}
	sort.Slice(modules, func(i, j int) bool {
		if modules[i].Name == modules[j].Name {
			return modules[i].SourceHint < modules[j].SourceHint
		}
		return modules[i].Name < modules[j].Name
	})
	return filterModules(modules, options), nil
}

func Backlog() models.PortingBacklog {
	return models.PortingBacklog{Title: "Tool surface", Modules: MustModules()}
}

func BacklogAtRoot(root string, options CatalogOptions) (models.PortingBacklog, error) {
	modules, err := Catalog(root, options)
	if err != nil {
		return models.PortingBacklog{}, err
	}
	return models.PortingBacklog{Title: "Tool surface", Modules: modules}, nil
}

func Get(name string) *models.PortingModule {
	return getFromList(MustModules(), name)
}

func GetAtRoot(root, name string, options CatalogOptions) (*models.PortingModule, error) {
	modules, err := Catalog(root, options)
	if err != nil {
		return nil, err
	}
	return getFromList(modules, name), nil
}

func List(simpleMode, includeMCP bool, permissionContext *permissions.ToolPermissionContext) []models.PortingModule {
	return filterModules(builtInModules(), CatalogOptions{
		SimpleMode:        simpleMode,
		IncludeMCP:        includeMCP,
		PermissionContext: permissionContext,
	})
}

func ListAtRoot(root string, simpleMode, includeMCP bool, permissionContext *permissions.ToolPermissionContext) ([]models.PortingModule, error) {
	return Catalog(root, CatalogOptions{
		SimpleMode:        simpleMode,
		IncludeMCP:        includeMCP,
		PermissionContext: permissionContext,
	})
}

func Find(query string, limit int) []models.PortingModule {
	return findInList(MustModules(), query, limit)
}

func FindAtRoot(root, query string, limit int, options CatalogOptions) ([]models.PortingModule, error) {
	modules, err := Catalog(root, options)
	if err != nil {
		return nil, err
	}
	return findInList(modules, query, limit), nil
}

func Execute(name, payload string) Execution {
	module := Get(name)
	if module == nil {
		return Execution{Name: name, Payload: payload, Handled: false, Message: "Unknown tool: " + name}
	}
	message := fmt.Sprintf("Registered tool %q from %s accepted payload %s.", module.Name, module.SourceHint, quote(payload))
	return Execution{Name: module.Name, SourceHint: module.SourceHint, Payload: payload, Handled: true, Message: message}
}

func ExecuteAtRoot(root, name, payload string, options CatalogOptions) (Execution, error) {
	module, err := GetAtRoot(root, name, options)
	if err != nil {
		return Execution{}, err
	}
	if module == nil {
		return Execution{Name: name, Payload: payload, Handled: false, Message: "Unknown tool: " + name}, nil
	}
	message := fmt.Sprintf("Registered tool %q from %s accepted payload %s.", module.Name, module.SourceHint, quote(payload))
	return Execution{Name: module.Name, SourceHint: module.SourceHint, Payload: payload, Handled: true, Message: message}, nil
}

func RenderIndex(limit int, query string) string {
	return renderIndex(MustModules(), limit, query)
}

func RenderIndexAtRoot(root string, limit int, query string, options CatalogOptions) (string, error) {
	modules, err := Catalog(root, options)
	if err != nil {
		return "", err
	}
	return renderIndex(modules, limit, query), nil
}

func builtInModules() []models.PortingModule {
	definitions := LiveDefinitions(nil)
	modules := make([]models.PortingModule, 0, len(definitions))
	for _, definition := range definitions {
		modules = append(modules, models.PortingModule{
			Name:           definition.Name,
			Responsibility: definition.Description,
			SourceHint:     "built-in",
			Status:         "live",
		})
	}
	return modules
}

func filterModules(modules []models.PortingModule, options CatalogOptions) []models.PortingModule {
	filtered := make([]models.PortingModule, 0, len(modules))
	for _, module := range modules {
		name := strings.ToLower(module.Name)
		source := strings.ToLower(module.SourceHint)
		if options.SimpleMode {
			switch name {
			case "read_file", "write_file", "edit_file":
			default:
				continue
			}
		}
		if !options.IncludeMCP && strings.Contains(source, "mcp/") {
			continue
		}
		if options.PermissionContext != nil && options.PermissionContext.Blocks(module.Name) {
			continue
		}
		filtered = append(filtered, module)
	}
	return filtered
}

func getFromList(modules []models.PortingModule, name string) *models.PortingModule {
	needle := strings.ToLower(strings.TrimSpace(name))
	for _, module := range modules {
		if strings.ToLower(module.Name) == needle {
			copy := module
			return &copy
		}
	}
	return nil
}

func findInList(modules []models.PortingModule, query string, limit int) []models.PortingModule {
	needle := strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 {
		limit = len(modules)
	}
	matches := make([]models.PortingModule, 0, limit)
	for _, module := range modules {
		if needle == "" ||
			strings.Contains(strings.ToLower(module.Name), needle) ||
			strings.Contains(strings.ToLower(module.SourceHint), needle) ||
			strings.Contains(strings.ToLower(module.Responsibility), needle) {
			matches = append(matches, module)
			if len(matches) == limit {
				break
			}
		}
	}
	return matches
}

func renderIndex(modules []models.PortingModule, limit int, query string) string {
	lines := []string{"Tool entries: " + itoa(len(modules)), ""}
	if strings.TrimSpace(query) != "" {
		lines = append(lines, "Filtered by: "+query, "")
		modules = findInList(modules, query, limit)
	} else if limit > 0 && len(modules) > limit {
		modules = modules[:limit]
	}
	for _, module := range modules {
		lines = append(lines, "- "+module.Name+" - "+module.SourceHint)
	}
	return strings.Join(lines, "\n")
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "\\'") + "'"
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
