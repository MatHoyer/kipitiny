package monitoring

import (
	"context"
	"fmt"
	"maps"
	netmail "net/mail"
	"slices"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/mail"
)

// InviteBody is the email carrying an invite link.
func (c *Monitoring) InviteBody(ctx context.Context, i domain.Invite, link string) mail.Body {
	who := "An admin"
	if u, err := c.store.GetUser(ctx, i.CreatedBy); err == nil {
		who = u.Username
	}
	brand := c.mgr.BrandName(ctx)
	where := "a " + brand + " server"
	switch {
	case brand != domain.DefaultBrandName && c.cfg.Domain != "":
		where = brand + " (" + c.cfg.Domain + ")"
	case brand != domain.DefaultBrandName:
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
	if i.Role == domain.RoleAdmin {
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

// CheckMailer fails when no email provider is set up (identity.Mailer).
func (c *Monitoring) CheckMailer(ctx context.Context) error {
	_, err := c.emailProvider(ctx, "")
	return err
}

// MailInvite emails an invite link (identity.Mailer).
func (c *Monitoring) MailInvite(ctx context.Context, to *netmail.Address, i domain.Invite, link string) error {
	return c.SendMail(ctx, "", []*netmail.Address{to}, c.InviteBody(ctx, i, link), "")
}
