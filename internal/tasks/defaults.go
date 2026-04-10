package tasks

func DefaultTasks() []PortingTask {
	return []PortingTask{
		{Name: "workspace-shape", Description: "Track active Go workspace modules and entrypoints"},
		{Name: "registry-audit", Description: "Inspect live command and tool registry coverage"},
		{Name: "traceability-audit", Description: "Verify mapped Go traceability targets exist"},
	}
}
