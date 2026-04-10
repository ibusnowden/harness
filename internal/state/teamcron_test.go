package state

import "testing"

func TestTeamAndCronRegistriesPersist(t *testing.T) {
	root := t.TempDir()

	teams := NewTeamRegistry()
	team := teams.Create("reviewers", []string{"task-1", "task-2"})
	if err := SaveTeamRegistry(root, teams); err != nil {
		t.Fatalf("save teams: %v", err)
	}
	reloadedTeams, err := LoadTeamRegistry(root)
	if err != nil {
		t.Fatalf("load teams: %v", err)
	}
	if got, ok := reloadedTeams.Get(team.TeamID); !ok || got.Name != "reviewers" || len(got.TaskIDs) != 2 {
		t.Fatalf("unexpected team reload: %#v ok=%v", got, ok)
	}

	crons := NewCronRegistry()
	description := "nightly summary"
	entry := crons.Create("@daily", "summarize", &description)
	if err := SaveCronRegistry(root, crons); err != nil {
		t.Fatalf("save crons: %v", err)
	}
	reloadedCrons, err := LoadCronRegistry(root)
	if err != nil {
		t.Fatalf("load crons: %v", err)
	}
	if got, ok := reloadedCrons.Get(entry.CronID); !ok || got.Schedule != "@daily" || got.Description == nil || *got.Description != description {
		t.Fatalf("unexpected cron reload: %#v ok=%v", got, ok)
	}
}
