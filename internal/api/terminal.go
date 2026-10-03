package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"golang.org/x/net/websocket"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// terminalMsg is what the browser sends: keystrokes or a new size. The
// server sends the output as binary frames, then one text frame with the
// exit code or the error that ended the session.
type terminalMsg struct {
	Type string `json:"type"` // "input" or "resize"
	Data string `json:"data,omitempty"`
	Cols uint   `json:"cols,omitempty"`
	Rows uint   `json:"rows,omitempty"`
}

type terminalEnd struct {
	Type  string `json:"type"` // "exit" or "error"
	Code  int    `json:"code"`
	Error string `json:"error,omitempty"`
}

func (a *API) serviceTerminal(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a.terminal(w, r, func(ctx context.Context, cols, rows uint) (*core.Terminal, error) {
		return a.core.OpenServiceTerminal(ctx, r.PathValue("id"), q.Get("container"), q.Get("shell"), cols, rows)
	})
}

func (a *API) serverTerminal(w http.ResponseWriter, r *http.Request) {
	a.terminal(w, r, func(ctx context.Context, cols, rows uint) (*core.Terminal, error) {
		return a.core.OpenServerTerminal(ctx, r.PathValue("id"), cols, rows)
	})
}

// terminal upgrades to a websocket and bridges it to the shell open returns.
// Errors after the upgrade go to the browser as an "error" message, since a
// WebSocket can't read the HTTP status.
func (a *API) terminal(w http.ResponseWriter, r *http.Request, open func(ctx context.Context, cols, rows uint) (*core.Terminal, error)) {
	cols, _ := strconv.ParseUint(r.URL.Query().Get("cols"), 10, 16)
	rows, _ := strconv.ParseUint(r.URL.Query().Get("rows"), 10, 16)
	srv := websocket.Server{
		// GETs skip the same-origin check in protect, but a websocket isn't
		// bound by CORS: refuse cross-site pages riding the session cookie.
		Handshake: func(_ *websocket.Config, r *http.Request) error {
			u, err := url.Parse(r.Header.Get("Origin"))
			if err != nil || u.Host != r.Host {
				return errors.New("cross-origin websocket refused")
			}
			return nil
		},
		Handler: func(ws *websocket.Conn) {
			defer ws.Close()
			ctx := r.Context()
			term, err := open(ctx, uint(cols), uint(rows))
			if err != nil {
				_ = websocket.JSON.Send(ws, terminalEnd{Type: "error", Error: a.message(err)})
				return
			}
			defer term.Close()
			// Closing the terminal ends the output copy below.
			stop := context.AfterFunc(ctx, func() { term.Close() })
			defer stop()

			ws.MaxPayloadBytes = 1 << 20
			gone := make(chan struct{})
			go func() {
				defer close(gone)
				defer term.Close()
				for {
					var m terminalMsg
					if err := websocket.JSON.Receive(ws, &m); err != nil {
						return // browser gone
					}
					switch m.Type {
					case "input":
						if _, err := io.WriteString(term, m.Data); err != nil {
							return
						}
					case "resize":
						_ = term.Resize(ctx, m.Cols, m.Rows)
					}
				}
			}()

			ws.PayloadType = websocket.BinaryFrame
			_, _ = io.Copy(ws, term)
			select {
			case <-gone:
				return // nobody to tell
			default:
			}
			code, err := term.Wait(ctx)
			end := terminalEnd{Type: "exit", Code: code}
			if err != nil {
				end = terminalEnd{Type: "error", Error: err.Error()}
			}
			_ = websocket.JSON.Send(ws, end)
		},
	}
	srv.ServeHTTP(w, r)
}

// message is the user-facing text of an error, as fail would send it.
func (a *API) message(err error) string {
	switch {
	case errors.Is(err, core.ErrForbidden):
		return reason(err, core.ErrForbidden)
	case errors.Is(err, core.ErrInvalid):
		return reason(err, core.ErrInvalid)
	case errors.Is(err, store.ErrNotFound):
		return "not found"
	}
	a.log.Warn("terminal failed", "err", err)
	return fmt.Sprintf("cannot open the terminal: %v", err)
}
