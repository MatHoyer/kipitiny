package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/MatHoyer/kipitiny/internal/ids"
	"github.com/MatHoyer/kipitiny/internal/storage"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// protonLoginTTL bounds a sign-in: waiting for it, then for the target that
// uses it to be saved.
const protonLoginTTL = 15 * time.Minute

type protonLogin struct {
	login  *storage.ProtonLogin
	cancel context.CancelFunc
}

// ProtonLogin is a sign-in to show: the URL to open, and how it's going.
type ProtonLogin struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	// Status is pending (waiting for the browser), done or failed.
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// StartProtonLogin starts a Proton sign-in for a Proton Drive target: open
// the URL on any device; once Status is done, pass the ID as the target's
// Login. Unused sign-ins are dropped after 15 minutes.
func (c *Core) StartProtonLogin() (ProtonLogin, error) {
	if !storage.ProtonAvailable(c.cfg.ProtonDriveCLI) {
		return ProtonLogin{}, fmt.Errorf("%w: proton-drive isn't installed on the manager", ErrInvalid)
	}
	// The sign-in outlives the request: it waits for the browser.
	lctx, cancel := context.WithTimeout(c.bg, protonLoginTTL)
	l, err := storage.StartProtonLogin(lctx, c.cfg.ProtonDriveCLI, filepath.Join(c.cfg.DataDir, "protondrive"), 30*time.Second)
	if err != nil {
		cancel()
		return ProtonLogin{}, err
	}
	id := ids.New()
	c.protonLogins.Store(id, &protonLogin{login: l, cancel: cancel})
	time.AfterFunc(protonLoginTTL, func() { c.dropProtonLogin(id) })
	return c.ProtonLoginStatus(id)
}

func (c *Core) ProtonLoginStatus(id string) (ProtonLogin, error) {
	v, ok := c.protonLogins.Load(id)
	if !ok {
		return ProtonLogin{}, fmt.Errorf("%w: this sign-in expired, start again", ErrInvalid)
	}
	l := v.(*protonLogin).login
	out := ProtonLogin{ID: id, URL: l.URL, Status: "pending"}
	select {
	case <-l.Done():
		if err := l.Err(); err != nil {
			out.Status, out.Error = "failed", err.Error()
		} else {
			out.Status = "done"
		}
	default:
	}
	return out, nil
}

// dropProtonLogin stops a sign-in and removes its files.
func (c *Core) dropProtonLogin(id string) {
	v, ok := c.protonLogins.LoadAndDelete(id)
	if !ok {
		return
	}
	pl := v.(*protonLogin)
	pl.cancel()
	<-pl.login.Done()
	os.RemoveAll(pl.login.Dir)
}

// applyProtonLogin gives a Proton Drive target the session of a finished
// sign-in; without one, an existing target keeps its session.
func (c *Core) applyProtonLogin(t *store.BackupTarget, id string) error {
	if id == "" {
		if t.Config[storage.ProtonSessionFile] == "" {
			return fmt.Errorf("%w: sign in to Proton first", ErrInvalid)
		}
		return nil
	}
	st, err := c.ProtonLoginStatus(id)
	if err != nil {
		return err
	}
	if st.Status != "done" {
		return fmt.Errorf("%w: the Proton sign-in isn't finished", ErrInvalid)
	}
	v, _ := c.protonLogins.Load(id)
	session, err := v.(*protonLogin).login.Session()
	if err != nil {
		return err
	}
	c.dropProtonLogin(id)
	t.Config = session
	return nil
}
