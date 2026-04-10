package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ascaris/internal/api"
)

func TestSaveLoadListAndSwitchSessions(t *testing.T) {
	root := t.TempDir()
	session := NewManagedSession("alpha", "sonnet")
	session.Messages = []api.InputMessage{
		api.UserTextMessage("hello"),
	}
	path, err := SaveManaged(session, root)
	if err != nil {
		t.Fatalf("save managed session: %v", err)
	}
	if !strings.Contains(path, filepath.Join(".ascaris", "sessions")) {
		t.Fatalf("expected hashed session path, got %s", path)
	}
	loaded, err := LoadManaged(root, "latest")
	if err != nil {
		t.Fatalf("load latest session: %v", err)
	}
	if loaded.Meta.SessionID != "alpha" {
		t.Fatalf("unexpected latest session: %#v", loaded.Meta)
	}
	items, err := List(root)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(items) != 1 || items[0].ID != "alpha" {
		t.Fatalf("unexpected session list: %#v", items)
	}
	if _, err := Switch(root, "alpha"); err != nil {
		t.Fatalf("switch session: %v", err)
	}
}

func TestForkDeleteAndExportSession(t *testing.T) {
	root := t.TempDir()
	session := NewManagedSession("seed", "sonnet")
	session.Messages = []api.InputMessage{api.UserTextMessage("seed")}
	if _, err := SaveManaged(session, root); err != nil {
		t.Fatalf("save seed: %v", err)
	}
	forked, err := Fork(root, "latest", "branch-a")
	if err != nil {
		t.Fatalf("fork session: %v", err)
	}
	if forked.Meta.Fork == nil || forked.Meta.Fork.ParentSessionID != "seed" {
		t.Fatalf("unexpected fork metadata: %#v", forked.Meta.Fork)
	}
	exported, err := Export(root, forked.Meta.SessionID, "")
	if err != nil {
		t.Fatalf("export session: %v", err)
	}
	if _, err := os.Stat(exported); err != nil {
		t.Fatalf("stat exported session: %v", err)
	}
	if err := Delete(root, forked.Meta.SessionID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	items, err := List(root)
	if err != nil {
		t.Fatalf("list sessions after delete: %v", err)
	}
	if len(items) != 1 || items[0].ID != "seed" {
		t.Fatalf("unexpected sessions after delete: %#v", items)
	}
}
