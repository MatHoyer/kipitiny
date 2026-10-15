package core

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestFixable(t *testing.T) {
	apt := parseHostFacts("pm=apt\nssh_port=2222\nssh_include=1\nssh_keys=1\n")
	if err := fixable("firewall", parseHostFacts("pm=apt\n")); err != nil {
		t.Errorf("firewall without sshd: %v", err)
	}
	for _, id := range []string{"updates", "auto_updates", "fail2ban", "firewall", "ssh_password", "ssh_root"} {
		if err := fixable(id, apt); err != nil {
			t.Errorf("%s on apt: %v", id, err)
		}
	}
	for name, c := range map[string]struct{ id, facts string }{
		"no keys":         {"ssh_password", "pm=apt\nssh_include=1\nssh_keys=0\n"},
		"no include":      {"ssh_root", "pm=apt\nssh_keys=3\n"},
		"firewall on dnf": {"firewall", "pm=dnf\nssh_port=22\n"},
		"no ssh port":     {"firewall", "pm=apt\nsshd=1\n"},
		"no package tool": {"fail2ban", "ssh_port=22\n"},
		"not fixable":     {"ports", "pm=apt\n"},
	} {
		if fixable(c.id, parseHostFacts(c.facts)) == nil {
			t.Errorf("%s: fixable", name)
		}
	}

	items := securityItems(parseHostFacts("pm=apt\nsshd=1\nauto_updates=0\nssh_passwordauthentication=yes\nssh_keys=0\nfail2ban=0\n"))
	for _, it := range items {
		if want := it.ID == "auto_updates" || it.ID == "fail2ban"; it.Fix != want {
			t.Errorf("%s: fix %v", it.ID, it.Fix)
		}
	}
}

func TestFixScripts(t *testing.T) {
	for _, pm := range []string{"apt", "dnf"} {
		f := parseHostFacts("pm=" + pm + "\nssh_port=2222; rm -rf /\nssh_port=22\nssh_port=2200\nssh_include=1\nssh_keys=1\n")
		if fw := fixScript("firewall", f); !strings.Contains(fw, "ufw allow 22/tcp\nufw allow 2200/tcp\n") {
			t.Errorf("%s: SSH ports not all allowed:\n%s", pm, fw)
		}
		for _, id := range []string{"updates", "auto_updates", "fail2ban", "firewall", "ssh_password", "ssh_root"} {
			s := fixScript(id, f)
			if strings.Contains(s, "rm -rf /") {
				t.Fatalf("%s/%s: host text in the script", pm, id)
			}
			_, inner, _ := strings.Cut(s, "<<'KIPITINY_FIX'\n")
			inner, _, _ = strings.Cut(inner, "\nKIPITINY_FIX\n")
			for _, script := range []string{s, inner} {
				cmd := exec.Command("sh", "-n")
				cmd.Stdin = strings.NewReader(script)
				if out, err := cmd.CombinedOutput(); err != nil || inner == "" {
					t.Errorf("%s/%s: %v %s", pm, id, err, out)
				}
			}
		}
	}
}

func TestFixServerSecurityRefuses(t *testing.T) {
	ctx := WithActor(context.Background(), Actor{Kind: "user", Name: "admin", Role: store.RoleAdmin})
	c := newTestCore(t, config.Config{})
	rep := SecurityReport{Items: []SecurityItem{{ID: "fail2ban", Status: SecurityWarn, Fix: true}, {ID: "ports", Status: SecurityWarn}}}
	b, _ := json.Marshal(rep)
	if err := c.store.SetSetting(ctx, securitySetting(store.LocalServerID), string(b)); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{nil, {"ports"}, {"fail2ban", "firewall"}} {
		if _, err := c.FixServerSecurity(ctx, store.LocalServerID, ids); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: %v", ids, err)
		}
	}
	c.securityFix.runs = map[string]*FixRun{store.LocalServerID: {IDs: []string{"fail2ban"}}}
	if _, err := c.FixServerSecurity(ctx, store.LocalServerID, []string{"fail2ban"}); !errors.Is(err, ErrBusy) {
		t.Errorf("while running: %v", err)
	}
	member := WithActor(ctx, Actor{Kind: "user", Name: "alice", Role: store.RoleMember})
	if _, err := c.FixServerSecurity(member, store.LocalServerID, []string{"fail2ban"}); err == nil {
		t.Error("a member fixed a server")
	}
}
