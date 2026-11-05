package ui

import (
	"reflect"
	"strings"
	"testing"
)

func TestQueryTables(t *testing.T) {
	for sql, want := range map[string][]string{
		"select * from auditlogapi_auditlog a order by a.created_at":            {"auditlogapi_auditlog"},
		"select 1 from public.a x join b on b.id = x.id left join c using (id)": {"public.a", "b", "c"},
		"select * from a, b as bb, c where a.x = 1":                             {"a", "b", "c"},
		"select * from (select id from inner_t) s":                              {"inner_t"},
		"select 'from fake' as s":                                               nil,
		"update t set x = 1 where id in (select id from u)":                     {"t", "u"},
		`select * from "Mixed Case"`:                                            {`"Mixed Case"`},
	} {
		if got := queryTables(sql); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %q want %q", sql, got, want)
		}
	}
}

func TestLayoutSQLBreaksTopLevelClausesOnly(t *testing.T) {
	got := layoutSQL("select a, (select max(x) from y where z) as m from t where a > 1 order by a limit 5")
	want := "select a, (select max(x) from y where z) as m\nfrom t\nwhere a > 1\norder by a\nlimit 5"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if kept := "select 1\n  from t"; layoutSQL(kept) != kept {
		t.Fatal("hand-formatted SQL must keep its layout")
	}
}

func TestHighlightSQLKeepsBracketsLiteral(t *testing.T) {
	s := newTestState(t, nil)
	s.sqlView.SetText(s.highlightSQL("select arr[red] from t"))
	if got := s.sqlView.GetText(true); !strings.Contains(got, "arr[red]") {
		t.Fatalf("brackets parsed as tags: %q", got)
	}
}

func TestZoomTogglesHideAndRestorePanes(t *testing.T) {
	s := newTestState(t, nil)
	typeKeys(s, "z")
	if !s.hideTables || s.zoomResults {
		t.Fatal("z should hide only the tables sidebar")
	}
	typeKeys(s, "z")
	if s.hideTables {
		t.Fatal("z again should restore the sidebar")
	}
	typeKeys(s, "Z")
	if !s.zoomResults || s.app.GetFocus() != s.grid {
		t.Fatal("Z should zoom results and focus the grid")
	}
	s.focusTables()
	if s.zoomResults || s.hideTables {
		t.Fatal("focusing tables must bring the sidebar back")
	}
}

func TestWrapSQLBreaksAtSpacesWithIndent(t *testing.T) {
	got := wrapSQL("select a->>'x' as one, b->>'y' as two", 20)
	want := "select a->>'x' as\n  one, b->>'y' as\n  two"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWrapSQLKeepsOperatorRunsWhole(t *testing.T) {
	got := wrapSQL("select x, a.data->'fields'->>'p_answerable' as p", 24)
	want := "select x,\n  a.data->'fields'->>'p_answerable'\n  as p"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
