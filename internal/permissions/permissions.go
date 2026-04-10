package permissions

import "strings"

type ToolPermissionContext struct {
	DenyNames    map[string]struct{}
	DenyPrefixes []string
}

func FromIterables(denyNames, denyPrefixes []string) ToolPermissionContext {
	names := map[string]struct{}{}
	for _, name := range denyNames {
		names[strings.ToLower(name)] = struct{}{}
	}
	prefixes := make([]string, 0, len(denyPrefixes))
	for _, prefix := range denyPrefixes {
		prefixes = append(prefixes, strings.ToLower(prefix))
	}
	return ToolPermissionContext{DenyNames: names, DenyPrefixes: prefixes}
}

func (c ToolPermissionContext) Blocks(toolName string) bool {
	lowered := strings.ToLower(toolName)
	if _, ok := c.DenyNames[lowered]; ok {
		return true
	}
	for _, prefix := range c.DenyPrefixes {
		if strings.HasPrefix(lowered, prefix) {
			return true
		}
	}
	return false
}
