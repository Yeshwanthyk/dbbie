package ui

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/catalog"
	"github.com/yeshwanthyk/dbbie/internal/config"
	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
	"github.com/yeshwanthyk/dbbie/internal/theme"
)

type Options struct {
	Kind       db.Kind
	DB         *sql.DB
	Theme      theme.Theme
	Limit      int
	InitialSQL string
	// Connection is a display name only, never a DSN.
	Connection      string
	SavedConnection bool
	Follow          bool
	Recent          int // number of stored entries to restore when following; zero restores none
	NoLog           bool
	Session         string
	Connections     []config.PublicConnection
	// OpenConnection returns a handle owned by the caller, and sanitized errors.
	OpenConnection func(context.Context, string) (db.Kind, *sql.DB, error)
	// RedactStrings protects UI results and logs, including ad-hoc credentials.
	RedactStrings func(...string) ([]string, error)
}

type tableRef struct{ Schema, Name string }
type gridPos struct{ Row, Col int }

func Run(ctx context.Context, opts Options) error {
	opts = NormalizeOptions(opts)
	ApplyTViewTheme(opts.Theme)
	app := tview.NewApplication()
	s := newState(ctx, app, opts)
	if opts.DB != nil {
		if err := s.loadCatalog(); err != nil {
			return errors.New("could not load database catalog")
		}
		if err := s.prepareLog(opts.Follow, opts.Recent); err != nil {
			return err
		}
	}
	if opts.Session != "" {
		if err := s.restoreSession(opts.Session); err != nil {
			return err
		}
	}
	root := s.layout()
	s.configureStartup(opts.InitialSQL, opts.Follow)
	app.SetInputCapture(s.captureInput)
	defer s.cancelAll()
	return app.SetRoot(root, true).EnableMouse(true).Run()
}

func (s *state) configureStartup(initialSQL string, follow bool) {
	started := false
	s.app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		s.screen = screen
		if !started {
			started = true
			s.startupPending = true
			// BeforeDraw holds tview's application mutex. Start on the UI
			// update queue instead: query setup reads focus and may change it.
			// Waiting for the first draw also avoids workers on a failed screen.
			go s.app.QueueUpdateDraw(func() {
				s.startupPending = false
				if s.quitting {
					s.stopWhenDrained()
					return
				}
				if s.db == nil {
					s.initialSQL = initialSQL
					s.pickConnections()
				} else {
					s.runInitialSQL(initialSQL)
					if follow {
						s.startFollow()
					}
				}
			})
		}
		return false
	})
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
	ctx                        context.Context
	app                        *tview.Application
	kind                       db.Kind
	db                         *sql.DB
	theme                      theme.Theme
	limit                      int
	connection                 string
	connectionSaved            bool
	tabs                       []*tab
	active                     int
	runner                     runnerHook
	pending                    int
	quitting                   bool
	startupPending             bool
	log                        *qlog.Log
	noLog                      bool
	logOffset                  int64
	seen                       map[string]bool
	deferred                   []qlog.Entry
	follow                     followHook
	following                  bool
	followCancel               context.CancelFunc
	agentNotice                string
	activeSession, sessionConn string
	connections                []config.PublicConnection
	openConnection             func(context.Context, string) (db.Kind, *sql.DB, error)
	followEnabled              bool
	followGeneration           uint64
	followWorkers              int
	initialSQL                 string
	startupRecent              int
	connecting                 bool
	connectCancel              context.CancelFunc
	awaitingConnection         bool
	picker                     *picker
	redactStrings              func(...string) ([]string, error)

	modeTag, modeColor, modeHints              string
	modeWidth                                  int
	headerWidth                                int
	modePrompt                                 *tview.InputField
	editOriginal                               string
	editing                                    bool
	recordWidth                                int
	header, status, why, sqlView               *tview.TextView
	modeInfo                                   *tview.TextView
	cellInfo, recordView                       *tview.TextView
	resultPane                                 *resultsFrame
	resultPages                                *tview.Pages
	viewRows                                   [][]any
	highlightedRow                             int
	naturalWidths                              []int
	gridViewportWidth                          int
	screen                                     tcell.Screen
	clipboardWriter                            io.Writer
	copiedUntil                                time.Time
	copyTimer                                  *time.Timer
	sidebar, grid                              *tview.Table
	query, command, search, tableFilter        *tview.InputField
	sidePane, queryPane, body, right, modeLine *tview.Flex
	pages, queryPages                          *tview.Pages
	prompts                                    *tview.Flex
	tables                                     []catalog.Table
	visibleTables                              []catalog.Table
	sideWidth                                  int
	multiSchema                                bool
	pendingKey                                 rune
	helpOpen                                   bool
	helpReturn                                 tview.Primitive
	message                                    string
	searchMatches                              []gridPos
	searchIndex                                int
}

func newState(ctx context.Context, app *tview.Application, opts Options) *state {
	opts = NormalizeOptions(opts)
	s := &state{ctx: ctx, app: app, kind: opts.Kind, db: opts.DB, theme: opts.Theme, limit: opts.Limit,
		connection: opts.Connection, connectionSaved: opts.SavedConnection, tabs: []*tab{{title: "query"}},
		noLog: opts.NoLog || os.Getenv("DBBIE_NO_LOG") == "1", seen: make(map[string]bool),
		connections: opts.Connections, openConnection: opts.OpenConnection, followEnabled: opts.Follow,
		awaitingConnection: opts.DB == nil && len(opts.Connections) > 0, startupRecent: opts.Recent}
	if s.connection == "" {
		s.connection = "adhoc"
		if opts.DB == nil && len(opts.Connections) > 0 {
			s.connection = "choose connection"
		}
	}
	s.redactStrings = opts.RedactStrings
	if s.redactStrings == nil {
		s.redactStrings = config.RedactStrings
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
