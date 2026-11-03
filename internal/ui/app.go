package ui

import (
	"context"
	"database/sql"
	"io"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/catalog"
	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/query"
	"github.com/yeshwanthyk/dbbie/internal/theme"
)

type Options struct {
	Kind       db.Kind
	DB         *sql.DB
	Theme      theme.Theme
	Limit      int
	InitialSQL string
	// Connection is a display name only, never a DSN. Callers without a saved
	// connection name get the neutral "session" label.
	Connection string
}

type tableRef struct{ Schema, Name string }
type gridPos struct{ Row, Col int }

func Run(ctx context.Context, opts Options) error {
	opts = NormalizeOptions(opts)
	ApplyTViewTheme(opts.Theme)
	app := tview.NewApplication()
	s := newState(ctx, app, opts)
	if err := s.loadCatalog(); err != nil {
		return err
	}
	root := s.layout()
	// Start only after the screen initialized. A failed terminal startup must
	// not leave a worker waiting on an event loop that never ran.
	started := false
	app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		s.screen = screen
		if !started {
			started = true
			s.runInitialSQL(opts.InitialSQL)
		}
		return false
	})
	app.SetInputCapture(s.captureInput)
	defer s.cancelAll()
	return app.SetRoot(root, true).EnableMouse(true).Run()
}

func NormalizeOptions(opts Options) Options {
	if opts.Limit <= 0 {
		opts.Limit = query.DefaultPreviewLimit
	}
	if opts.Limit > query.MaxPreviewLimit {
		opts.Limit = query.MaxPreviewLimit
	}
	return opts
}

func ApplyTViewTheme(t theme.Theme) {
	tview.Styles.PrimitiveBackgroundColor = t.TCell(t.Background)
	tview.Styles.ContrastBackgroundColor = t.TCell(t.Panel)
	tview.Styles.MoreContrastBackgroundColor = t.TCell(t.Primary)
	tview.Styles.BorderColor = t.TCell(t.Border)
	tview.Styles.TitleColor = t.TCell(t.Muted)
	tview.Styles.GraphicsColor = t.TCell(t.Border)
	tview.Styles.PrimaryTextColor = t.TCell(t.Text)
	tview.Styles.SecondaryTextColor = t.TCell(t.Muted)
	tview.Styles.TertiaryTextColor = t.TCell(t.Accent)
	tview.Styles.InverseTextColor = t.TCell(t.Background)
	tview.Styles.ContrastSecondaryTextColor = t.TCell(t.Muted)
	// tview normally draws double lines when focused. Focus here changes color,
	// not geometry: all panes use the same single, rounded border.
	tview.Borders.TopLeft = '╭'
	tview.Borders.TopRight = '╮'
	tview.Borders.BottomLeft = '╰'
	tview.Borders.BottomRight = '╯'
	tview.Borders.HorizontalFocus = tview.Borders.Horizontal
	tview.Borders.VerticalFocus = tview.Borders.Vertical
	tview.Borders.TopLeftFocus = tview.Borders.TopLeft
	tview.Borders.TopRightFocus = tview.Borders.TopRight
	tview.Borders.BottomLeftFocus = tview.Borders.BottomLeft
	tview.Borders.BottomRightFocus = tview.Borders.BottomRight
}

// Mutable state, including tabs and query completions, belongs to tview's event
// loop. Background workers only receive immutable query inputs and contexts.
type state struct {
	ctx        context.Context
	app        *tview.Application
	kind       db.Kind
	db         *sql.DB
	theme      theme.Theme
	limit      int
	connection string
	tabs       []*tab
	active     int
	runner     runnerHook
	pending    int
	quitting   bool

	header, status, why, sqlView        *tview.TextView
	cellInfo, recordView                *tview.TextView
	resultPane                          *resultsFrame
	resultPages                         *tview.Pages
	viewRows                            [][]any
	highlightedRow                      int
	naturalWidths                       []int
	gridViewportWidth                   int
	screen                              tcell.Screen
	clipboardWriter                     io.Writer
	copiedUntil                         time.Time
	copyTimer                           *time.Timer
	sidebar, grid                       *tview.Table
	query, command, search, tableFilter *tview.InputField
	sidePane, queryPane, body, modeLine *tview.Flex
	pages, queryPages                   *tview.Pages
	prompts                             *tview.Flex
	tables                              []catalog.Table
	visibleTables                       []catalog.Table
	sideWidth                           int
	multiSchema                         bool
	pendingKey                          rune
	helpOpen                            bool
	helpReturn                          tview.Primitive
	message                             string
	searchMatches                       []gridPos
	searchIndex                         int
}

func newState(ctx context.Context, app *tview.Application, opts Options) *state {
	opts = NormalizeOptions(opts)
	s := &state{ctx: ctx, app: app, kind: opts.Kind, db: opts.DB, theme: opts.Theme, limit: opts.Limit, connection: opts.Connection, tabs: []*tab{{title: "query"}}}
	if s.connection == "" {
		s.connection = "session"
	}
	s.runner = s.backgroundRunner
	return s
}

func (s *state) runInitialSQL(sqlText string) {
	sqlText = strings.TrimSpace(sqlText)
	if sqlText == "" {
		return
	}
	s.query.SetText(sqlText)
	s.runQuery(sqlText)
}

func quoteIdent(kind db.Kind, ident string) string {
	if kind == db.MySQL {
		return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
