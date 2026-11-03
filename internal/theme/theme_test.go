package theme

import (
	"github.com/gdamore/tcell/v2"
	"testing"
)

func TestResolveDefault(t *testing.T) {
	got, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Name != "midnight" {
		t.Fatalf("theme = %q, want midnight", got.Name)
	}
}

func TestResolveRejectsUnknown(t *testing.T) {
	if _, err := Resolve("neon"); err == nil {
		t.Fatal("expected unknown theme error")
	}
}

func TestShellColorsAvailableInEveryTheme(t *testing.T) {
	for _, name := range Names() {
		theme, err := Resolve(name)
		if err != nil {
			t.Fatal(err)
		}
		for field, color := range map[string]string{"selection": theme.Selection, "success": theme.Success, "agent": theme.Agent} {
			if color == "" || theme.TCell(color) == tcell.ColorDefault {
				t.Fatalf("%s theme has no %s color", name, field)
			}
		}
	}
}

func TestLipGlossStatusStyle(t *testing.T) {
	got, err := Resolve("contrast")
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusStyle().Render("dbbie") == "" {
		t.Fatal("empty rendered style")
	}
}
