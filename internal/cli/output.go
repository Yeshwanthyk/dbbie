package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/yeshwanthyk/dbbie/internal/config"
	"github.com/yeshwanthyk/dbbie/internal/qlog"
)

type cliError struct {
	code    string
	message string
}

func (e *cliError) Error() string { return e.message }

func commandError(code, message string) error { return &cliError{code: code, message: message} }

func errorCode(err error) string {
	var typed *cliError
	if errors.As(err, &typed) {
		return typed.code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "invalid"
}

func databaseError(err error) error {
	code := "db"
	var timeout interface{ Timeout() bool }
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		code = "timeout"
	}
	return commandError(code, err.Error())
}

func errorDetail(err error) *qlog.Error {
	return &qlog.Error{Code: errorCode(err), Message: err.Error()}
}

func isTerminal(w io.Writer) bool {
	if redacted, ok := w.(redactedWriter); ok {
		return isTerminal(redacted.Writer)
	}
	fd, ok := w.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(fd.Fd()))
}

// Inspect boolean flags without mistaking SQL, a reason, or another flag value
// for --json. Individual commands still validate their own flag syntax.
func outputJSON(args []string, stdout io.Writer) bool {
	jsonOut := !isTerminal(stdout)
	if len(args) == 0 {
		return jsonOut
	}
	start := 1
	if args[0] == "connect" && len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		start = 4 // the first three arguments are positional, including a secret
	}
	for i := start; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		name, value, assigned := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		if name == "json" {
			if !assigned {
				jsonOut = true
			} else if parsed, err := strconv.ParseBool(value); err == nil {
				jsonOut = parsed
			}
		} else if !assigned && name != "no-log" && name != "allow-write" && name != "help" && name != "h" {
			i++
		}
	}
	return jsonOut
}

func commandSecrets(args []string) []string {
	var secrets []string
	if len(args) >= 4 && args[0] == "connect" && !strings.HasPrefix(args[1], "-") {
		secrets = append(secrets, args[3])
	}
	for i := 1; i < len(args); i++ {
		name, value, assigned := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		if name == "dsn" {
			if assigned {
				secrets = append(secrets, value)
			} else if i+1 < len(args) {
				secrets = append(secrets, args[i+1])
			}
		}
	}
	if p, err := config.Path(); err == nil {
		if cfg, err := config.Load(p); err == nil {
			for _, conn := range cfg.Connections {
				secrets = append(secrets, conn.DSN)
			}
		}
	}
	return secrets
}

var dsnURL = regexp.MustCompile(`(?i)(?:postgres(?:ql)?|mysql)://[^\s"'<>]+`)

func redact(s string, secrets []string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "[redacted]")
		// JSON escapes '&' and other characters; cover encoded output as well.
		if encoded, err := json.Marshal(secret); err == nil {
			s = strings.ReplaceAll(s, string(encoded[1:len(encoded)-1]), "[redacted]")
		}
	}
	return dsnURL.ReplaceAllString(s, "[redacted]")
}

type redactedWriter struct {
	io.Writer
	secrets []string
}

func redactValue(value any, secrets []string) any {
	switch v := value.(type) {
	case string:
		return redact(v, secrets)
	case []any:
		for i := range v {
			v[i] = redactValue(v[i], secrets)
		}
	case map[string]any:
		for key, item := range v {
			v[key] = redactValue(item, secrets)
		}
	}
	return value
}

func (w redactedWriter) Write(p []byte) (int, error) {
	out := p
	redacted := redact(string(p), w.secrets)
	if redacted == string(p) {
		// Preserve human text and the encoder's field ordering when unchanged.
	} else if json.Valid(p) {
		// Redact strings, not serialized bytes: a DSN inside escaped quotes must
		// not consume an escape and break JSON, nor change numeric precision.
		decoder := json.NewDecoder(bytes.NewReader(p))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return 0, err
		}
		var err error
		out, err = json.MarshalIndent(redactValue(value, w.secrets), "", "  ")
		if err != nil {
			return 0, err
		}
		out = append(out, '\n')
	} else {
		out = []byte(redacted)
	}
	n, err := w.Writer.Write(out)
	if err == nil && n != len(out) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return 0, err
	}
	return len(p), nil
}
