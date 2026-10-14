package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	netmail "net/mail"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/MatHoyer/kipitiny/internal/mail"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Users and invites: admins manage who signs in and what members may do.

// InviteTTL is how long an invite link works.
const InviteTTL = 7 * 24 * time.Hour

// UserView is a user with their grants, for admins.
type UserView struct {
	store.User
	// Grants maps project IDs to a member's permissions.
	Grants      map[string][]string `json:"grants"`
	TOTPEnabled bool                `json:"totpEnabled"`
}

// UserAccess is what a user may do: a role, and for a member their
// server features and permissions per project.
type UserAccess struct {
	Role     store.Role          `json:"role"`
	Features []string            `json:"features"`
	Grants   map[string][]string `json:"grants"`
}

// normalized validates a and drops what doesn't apply: an admin has no
// features or grants, a project without permissions no grant.
func (c *Core) normalized(ctx context.Context, a UserAccess) (UserAccess, error) {
	if a.Role != store.RoleAdmin && a.Role != store.RoleMember {
		return UserAccess{}, fmt.Errorf("%w: role must be admin or member", ErrInvalid)
	}
	out := UserAccess{Role: a.Role, Features: []string{}, Grants: map[string][]string{}}
	if a.Role == store.RoleAdmin {
		return out, nil
	}
	for _, f := range a.Features {
		if !slices.Contains(Features, Feature(f)) {
			return UserAccess{}, fmt.Errorf("%w: unknown feature %q", ErrInvalid, f)
		}
		if !slices.Contains(out.Features, f) {
			out.Features = append(out.Features, f)
		}
	}
	for projectID, perms := range a.Grants {
		var ps []string
		for _, p := range perms {
			if !slices.Contains(Perms, Perm(p)) {
				return UserAccess{}, fmt.Errorf("%w: unknown permission %q", ErrInvalid, p)
			}
			if !slices.Contains(ps, p) {
				ps = append(ps, p)
			}
		}
		if len(ps) == 0 {
			continue
		}
		if _, err := c.store.GetProject(ctx, projectID); err != nil {
			return UserAccess{}, fmt.Errorf("project %s: %w", projectID, err)
		}
		out.Grants[projectID] = ps
	}
	return out, nil
}

func (c *Core) userView(ctx context.Context, u store.User) (UserView, error) {
	v := UserView{User: u, Grants: map[string][]string{}, TOTPEnabled: u.TOTPSecret != ""}
	gs, err := c.store.ListGrants(ctx, u.ID)
	for _, g := range gs {
		v.Grants[g.ProjectID] = g.Perms
	}
	return v, err
}

// ListUsers returns every user with their access; admins only.
func (c *Core) ListUsers(ctx context.Context) ([]UserView, error) {
	if err := RequireAdmin(ctx); err != nil {
		return nil, err
	}
	us, err := c.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserView, len(us))
	for i, u := range us {
		if out[i], err = c.userView(ctx, u); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SetUserAccess replaces a user's role, features and grants; admins only.
// The last admin stays one.
func (c *Core) SetUserAccess(ctx context.Context, id string, in UserAccess) (UserView, error) {
	if err := RequireAdmin(ctx); err != nil {
		return UserView{}, err
	}
	in, err := c.normalized(ctx, in)
	if err != nil {
		return UserView{}, err
	}
	c.setupMu.Lock()
	defer c.setupMu.Unlock()
	u, err := c.store.GetUser(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	if u.Role == store.RoleAdmin && in.Role != store.RoleAdmin {
		if err := c.keepAnAdmin(ctx, id); err != nil {
			return UserView{}, err
		}
	}
	if u, err = c.store.UpdateUserAccess(ctx, id, in.Role, in.Features); err != nil {
		return UserView{}, err
	}
	old, err := c.store.ListGrants(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	for _, g := range old {
		if _, kept := in.Grants[g.ProjectID]; !kept {
			if err := c.store.SetGrant(ctx, store.ProjectGrant{UserID: id, ProjectID: g.ProjectID}); err != nil {
				return UserView{}, err
			}
		}
	}
	for projectID, perms := range in.Grants {
		if err := c.store.SetGrant(ctx, store.ProjectGrant{UserID: id, ProjectID: projectID, Perms: perms}); err != nil {
			return UserView{}, err
		}
	}
	return c.userView(ctx, u)
}

// DeleteUser removes a user with their sessions and tokens; admins only,
// not themselves.
func (c *Core) DeleteUser(ctx context.Context, id string) error {
	if err := RequireAdmin(ctx); err != nil {
		return err
	}
	if a, _ := ActorFrom(ctx); a.UserID == id {
		return fmt.Errorf("%w: you can't delete your own account", ErrInvalid)
	}
	c.setupMu.Lock()
	defer c.setupMu.Unlock()
	u, err := c.store.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if u.Role == store.RoleAdmin {
		if err := c.keepAnAdmin(ctx, id); err != nil {
			return err
		}
	}
	return c.store.DeleteUser(ctx, id)
}

// keepAnAdmin fails when id is the only admin.
func (c *Core) keepAnAdmin(ctx context.Context, id string) error {
	us, err := c.store.ListUsers(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(us, func(u store.User) bool { return u.Role == store.RoleAdmin && u.ID != id }) {
		return fmt.Errorf("%w: this is the last admin; make another user admin first", ErrInvalid)
	}
	return nil
}

// MakeAdmin makes a user an admin, for the CLI when no admin can sign in.
func (c *Core) MakeAdmin(ctx context.Context, username string) error {
	u, err := c.store.GetUserByUsername(ctx, username)
	if err != nil {
		return err
	}
	if _, err := c.store.UpdateUserAccess(ctx, u.ID, store.RoleAdmin, []string{}); err != nil {
		return err
	}
	return c.store.DeleteUserSessions(ctx, u.ID) // sessions pick the role up on the next sign-in anyway
}

// NewInvite is an invite with its token, shown once.
type NewInvite struct {
	store.Invite
	Token string `json:"token"`
	// EmailError is why the invite couldn't be emailed; the link still works.
	EmailError string `json:"emailError,omitempty"`
}

// InviteInput is the access an invite gives, and optionally where to email
// it.
type InviteInput struct {
	UserAccess
	// Email sends the link there through the SMTP server.
	Email string `json:"email"`
	// Link is the invite page's URL as the admin's browser sees it; the
	// token is appended as its fragment. Required with Email.
	Link string `json:"link"`
}

// CreateInvite makes a single-use link that creates an account with this
// access, and emails it when asked; admins only.
func (c *Core) CreateInvite(ctx context.Context, in InviteInput) (NewInvite, error) {
	if err := RequireAdmin(ctx); err != nil {
		return NewInvite{}, err
	}
	access, err := c.normalized(ctx, in.UserAccess)
	if err != nil {
		return NewInvite{}, err
	}
	var to *netmail.Address
	if in.Email = strings.TrimSpace(in.Email); in.Email != "" {
		if to, err = mail.ParseAddress(in.Email); err != nil {
			return NewInvite{}, mailErr(err)
		}
		if u, err := url.Parse(in.Link); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Fragment != "" {
			return NewInvite{}, fmt.Errorf("%w: link must be the http(s) URL of the invite page", ErrInvalid)
		}
		if _, ok := c.smtpConfig(ctx); !ok {
			return NewInvite{}, ErrNoEmail
		}
		in.Email = to.Address
	}
	a, _ := ActorFrom(ctx)
	token := randomToken(32)
	i, err := c.store.CreateInvite(ctx, store.Invite{TokenHash: hashToken(token), Role: access.Role, Features: access.Features,
		Grants: access.Grants, CreatedBy: a.UserID, Email: in.Email, ExpiresAt: time.Now().Add(InviteTTL)})
	if err != nil {
		return NewInvite{}, err
	}
	out := NewInvite{Invite: i, Token: token}
	if to != nil {
		if err := c.sendMail(ctx, []*netmail.Address{to}, c.inviteBody(ctx, i, in.Link+"#"+token), ""); err != nil {
			c.log.Warn("invite not emailed", "to", in.Email, "err", err)
			out.EmailError = err.Error()
		}
	}
	return out, nil
}

// inviteBody is the email carrying an invite link.
func (c *Core) inviteBody(ctx context.Context, i store.Invite, link string) mail.Body {
	who := "An admin"
	if u, err := c.store.GetUser(ctx, i.CreatedBy); err == nil {
		who = u.Username
	}
	brand := c.BrandName(ctx)
	where := "a " + brand + " server"
	switch {
	case brand != DefaultBrandName && c.cfg.Domain != "":
		where = brand + " (" + c.cfg.Domain + ")"
	case brand != DefaultBrandName:
		where = brand
	case c.cfg.Domain != "":
		where = "the kipitiny server " + c.cfg.Domain
	}
	b := mail.Body{
		Title: "You're invited to " + brand,
		Paragraphs: []string{
			fmt.Sprintf("%s invited you to %s. Open the link to pick your username and password.", who, where),
			fmt.Sprintf("It works once, until %s. If you weren't expecting it, ignore this email.", i.ExpiresAt.UTC().Format("January 2, 2006 15:04 UTC")),
		},
		Button: &mail.Link{Label: "Accept the invite", URL: link},
		Footer: c.mailFooter(ctx),
	}
	if i.Role == store.RoleAdmin {
		b.Fields = []mail.Field{{Name: "Role", Value: "Admin"}}
	} else {
		var names []string
		for _, id := range slices.Sorted(maps.Keys(i.Grants)) {
			if p, err := c.store.GetProject(ctx, id); err == nil {
				names = append(names, p.Name)
			}
		}
		b.Fields = []mail.Field{{Name: "Role", Value: "Member"}, {Name: "Projects", Value: strings.Join(names, ", ")}}
	}
	return b
}

// ListInvites returns the invites not used yet; admins only.
func (c *Core) ListInvites(ctx context.Context) ([]store.Invite, error) {
	if err := RequireAdmin(ctx); err != nil {
		return nil, err
	}
	return c.store.ListInvites(ctx)
}

// DeleteInvite revokes an invite; admins only.
func (c *Core) DeleteInvite(ctx context.Context, id string) error {
	if err := RequireAdmin(ctx); err != nil {
		return err
	}
	return c.store.DeleteInvite(ctx, id)
}

// InviteInfo is what someone opening an invite link sees.
type InviteInfo struct {
	Role      store.Role `json:"role"`
	ExpiresAt time.Time  `json:"expiresAt"`
	// Projects names the projects it grants.
	Projects []string `json:"projects"`
	// SSO lists the providers the account can sign in with instead of a
	// password; with SSORequired, the only way to accept it.
	SSO         []SSOButton `json:"sso"`
	SSORequired bool        `json:"ssoRequired"`
}

// Invite describes a valid invite; ErrNotFound for an unknown, used or
// expired one.
func (c *Core) Invite(ctx context.Context, token string) (InviteInfo, error) {
	i, err := c.store.GetInviteByHash(ctx, hashToken(strings.TrimSpace(token)))
	if err != nil {
		return InviteInfo{}, inviteErr(err)
	}
	info := InviteInfo{Role: i.Role, ExpiresAt: i.ExpiresAt, Projects: []string{}, SSO: c.SSOButtons(ctx), SSORequired: c.SSORequired(ctx)}
	for _, id := range slices.Sorted(maps.Keys(i.Grants)) {
		if p, err := c.store.GetProject(ctx, id); err == nil {
			info.Projects = append(info.Projects, p.Name)
		}
	}
	return info, nil
}

// AcceptInvite creates the invited account and returns a session for it.
func (c *Core) AcceptInvite(ctx context.Context, token, username, password string) (store.User, string, error) {
	if c.SSORequired(ctx) {
		return store.User{}, "", ErrSSORequired
	}
	i, err := c.store.GetInviteByHash(ctx, hashToken(strings.TrimSpace(token)))
	if err != nil {
		return store.User{}, "", inviteErr(err)
	}
	if err := c.checkNewUsername(ctx, username); err != nil {
		return store.User{}, "", err
	}
	if err := validatePassword(password); err != nil {
		return store.User{}, "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return store.User{}, "", err
	}
	u, err := c.store.AcceptInvite(ctx, i.ID, store.User{Username: username, PasswordHash: string(hash)}, nil)
	if err != nil {
		return store.User{}, "", inviteErr(err)
	}
	session, err := c.newSession(ctx, u.ID)
	return u, session, err
}

// checkNewUsername fails unless username is valid and free.
func (c *Core) checkNewUsername(ctx context.Context, username string) error {
	if !nameRe.MatchString(username) {
		return fmt.Errorf("%w: username must be lowercase letters, digits and dashes", ErrInvalid)
	}
	if _, err := c.store.GetUserByUsername(ctx, username); err == nil {
		return fmt.Errorf("%w: username %s is taken", ErrInvalid, username)
	}
	return nil
}

func inviteErr(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: this invite link is invalid, used or expired; ask for a new one", store.ErrNotFound)
	}
	return err
}
