package theme

import "testing"

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

func TestLipGlossStatusStyle(t *testing.T) {
	got, err := Resolve("contrast")
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusStyle().Render("dbbie") == "" {
		t.Fatal("empty rendered style")
	}
}
