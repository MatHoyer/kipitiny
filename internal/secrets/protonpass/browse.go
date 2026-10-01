package protonpass

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/secrets"
)

func (c *Client) Vaults(ctx context.Context) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out, err := c.run(ctx, nil, "vault", "list", "--output", "json")
	if err != nil {
		return nil, err
	}
	var res struct {
		Vaults []struct {
			Name string `json:"name"`
		} `json:"vaults"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	names := make([]string, len(res.Vaults))
	for i, v := range res.Vaults {
		names[i] = v.Name
	}
	slices.Sort(names)
	return names, nil
}

// listedItem is an item of `item list --show-secrets`: field names come
// with their values, which are dropped right away.
type listedItem struct {
	ID      string `json:"id"`
	ShareID string `json:"share_id"`
	Content struct {
		Title   string `json:"title"`
		Note    string `json:"note"`
		Content struct {
			Login *struct {
				Email    string `json:"email"`
				Username string `json:"username"`
				Password string `json:"password"`
				TOTP     string `json:"totp_uri"`
			} `json:"Login"`
			Custom *struct {
				Sections []struct {
					Name   string      `json:"section_name"`
					Fields []namedField `json:"section_fields"`
				} `json:"sections"`
			} `json:"Custom"`
		} `json:"content"`
		ExtraFields []namedField `json:"extra_fields"`
	} `json:"content"`
}

type namedField struct {
	Name string `json:"name"`
}

func (c *Client) Items(ctx context.Context, vault string) ([]secrets.Item, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out, err := c.run(ctx, nil, "item", "list", vault, "--filter-state", "active",
		"--sort-by", "alphabetic-asc", "--output", "json", "--show-secrets")
	if err != nil {
		return nil, err
	}
	var res struct {
		Items []listedItem `json:"items"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	items := make([]secrets.Item, 0, len(res.Items))
	for _, it := range res.Items {
		if f := itemFields(vault, it); len(f) > 0 {
			items = append(items, secrets.Item{Title: it.Content.Title, Fields: f})
		}
	}
	return items, nil
}

// itemFields lists the non-empty fields of it that a reference can name.
func itemFields(vault string, it listedItem) []secrets.Field {
	// Names are readable but can't hold a slash; IDs always work.
	base := Scheme + "://" + vault + "/" + it.Content.Title + "/"
	if strings.Contains(vault, "/") || strings.Contains(it.Content.Title, "/") || it.Content.Title == "" {
		base = Scheme + "://" + it.ShareID + "/" + it.ID + "/"
	}
	var out []secrets.Field
	add := func(name string, set bool) {
		if set && name != "" {
			out = append(out, secrets.Field{Name: name, Ref: base + name})
		}
	}
	c := it.Content
	if l := c.Content.Login; l != nil {
		add("username", l.Username != "")
		add("email", l.Email != "")
		add("password", l.Password != "")
		add("totp", l.TOTP != "")
	}
	if cu := c.Content.Custom; cu != nil {
		for _, s := range cu.Sections {
			for _, f := range s.Fields {
				if s.Name == "" {
					add(f.Name, true)
				} else {
					add(s.Name+"."+f.Name, true)
				}
			}
		}
	}
	for _, f := range c.ExtraFields {
		add(f.Name, true)
	}
	add("note", c.Note != "")
	return out
}
