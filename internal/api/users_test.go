package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// json sends a request and decodes the response into out.
func (c *client) json(method, path, body string, out any) int {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.srv.URL+path, strings.NewReader(body))
	res, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestUsersAndInvites(t *testing.T) {
	srv := newServer(t)
	admin := newClient(t, srv)
	expect(t, "setup", admin.do("POST", "/api/auth/setup", `{"setupToken":"setup-secret","username":"admin","password":"correct-horse"}`), 201)

	var inv struct{ ID, Token string }
	expect(t, "unknown feature", admin.json("POST", "/api/invites", `{"role":"member","features":["nope"]}`, nil), 400)
	expect(t, "grant on a missing project", admin.json("POST", "/api/invites", `{"role":"member","grants":{"nope":["view"]}}`, nil), 404)
	expect(t, "invite", admin.json("POST", "/api/invites", `{"role":"member","features":["storage"]}`, &inv), 201)
	var invites []struct{ ID string }
	if admin.json("GET", "/api/invites", "", &invites); len(invites) != 1 || invites[0].ID != inv.ID {
		t.Fatalf("invites: %+v", invites)
	}

	alice := newClient(t, srv)
	expect(t, "wrong invite", alice.json("POST", "/api/auth/invite", `{"token":"nope"}`, nil), 404)
	var info struct{ Role string }
	if code := alice.json("POST", "/api/auth/invite", `{"token":"`+inv.Token+`"}`, &info); code != 200 || info.Role != "member" {
		t.Fatalf("invite info: %d %+v", code, info)
	}
	expect(t, "taken username", alice.json("POST", "/api/auth/invite/accept",
		`{"token":"`+inv.Token+`","username":"admin","password":"correct-horse"}`, nil), 400)
	expect(t, "accept", alice.json("POST", "/api/auth/invite/accept",
		`{"token":"`+inv.Token+`","username":"alice","password":"correct-horse"}`, nil), 201)
	expect(t, "used invite", newClient(t, srv).json("POST", "/api/auth/invite/accept",
		`{"token":"`+inv.Token+`","username":"bob","password":"correct-horse"}`, nil), 404)

	var acc struct {
		Role     string
		Features []string
	}
	if alice.json("GET", "/api/account", "", &acc); acc.Role != "member" || len(acc.Features) != 1 {
		t.Fatalf("member account: %+v", acc)
	}
	expect(t, "a member lists no users", alice.json("GET", "/api/users", "", nil), 403)
	expect(t, "a member sees what their feature allows", alice.json("GET", "/api/storage", "", nil), 200)
	expect(t, "a member can't invite", alice.json("POST", "/api/invites", `{"role":"admin"}`, nil), 403)

	var users []struct {
		ID, Username, Role string
	}
	admin.json("GET", "/api/users", "", &users)
	if len(users) != 2 || users[1].Username != "alice" {
		t.Fatalf("users: %+v", users)
	}
	adminID, aliceID := users[0].ID, users[1].ID
	expect(t, "the last admin stays one", admin.json("PUT", "/api/users/"+adminID, `{"role":"member"}`, nil), 400)
	expect(t, "no deleting yourself", admin.json("DELETE", "/api/users/"+adminID, "", nil), 400)
	expect(t, "promote", admin.json("PUT", "/api/users/"+aliceID, `{"role":"admin"}`, nil), 200)
	expect(t, "an admin now", alice.json("GET", "/api/users", "", nil), 200)
	expect(t, "demote the other admin", alice.json("PUT", "/api/users/"+adminID, `{"role":"member"}`, nil), 200)
	expect(t, "delete", alice.json("DELETE", "/api/users/"+adminID, "", nil), 204)
	expect(t, "deleted user is signed out", admin.json("GET", "/api/projects", "", nil), 401)
}
