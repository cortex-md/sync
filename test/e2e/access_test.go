//go:build e2e

package e2e

import (
	"crypto/rand"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"
)

func addMember(t *testing.T, owner *client, vaultID string, member *client, role string) {
	t.Helper()
	status, body, _ := owner.do("POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{"invitee_email": member.email, "role": role})
	token, _ := jsonMap(body)["invite_token"].(string)
	if status != 201 || token == "" {
		t.Fatalf("invite %s: %d %s", member.email, status, body)
	}
	status, body, _ = member.do("POST", "/vaults/v1/invites/accept", map[string]string{"token": token})
	if status != 200 {
		t.Fatalf("accept %s: %d %s", member.email, status, body)
	}
}

func listPaths(c *client, base, vaultID string) []string {
	_, body, _ := c.doOn(base, "GET", "/sync/v1/vaults/"+vaultID+"/files/list", nil, nil)
	var paths []string
	for _, f := range jsonList(body) {
		paths = append(paths, f["file_path"].(string))
	}
	return paths
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func TestGroupsAndPathAccess(t *testing.T) {
	check := checker(t)
	alice := newUser(t, urlA, "Accessowner")
	bob := newUser(t, urlB, "Accesseditor")
	carol := newUser(t, urlA, "Accessviewer")
	vaultID := createVault(t, alice, "Access")
	addMember(t, alice, vaultID, bob, "editor")
	addMember(t, alice, vaultID, carol, "viewer")
	base := "/sync/v1/vaults/" + vaultID
	for _, p := range []string{"open.md", "team/a.md", "restricted/secret.md", "restricted/sub/deep.md"} {
		status, body, _ := alice.upload(urlA, vaultID, p, []byte(p))
		check("seed "+p, status == 201, status, string(body))
	}

	bobSSE := openSSE(bob, urlB, vaultID)
	status, body, _ := alice.do("PUT", "/vaults/v1/"+vaultID+"/access", map[string]any{"path": "restricted/", "restricted": true, "entries": []any{}})
	check("owner restricts a folder", status == 200 && jsonMap(body)["restricted"] == true, status, string(body))
	changed := bobSSE.waitFor("access_changed", 5*time.Second)
	check("access_changed reaches a member on the other instance", changed != nil, changed)

	ok := eventually(3*time.Second, func() bool {
		paths := listPaths(bob, urlB, vaultID)
		return contains(paths, "open.md") && !contains(paths, "restricted/secret.md") && !contains(paths, "restricted/sub/deep.md")
	})
	check("restricted folder is hidden from an editor", ok, listPaths(bob, urlB, vaultID))
	status, _, _ = bob.doOn(urlB, "GET", base+"/files?path="+url.QueryEscape("restricted/secret.md"), nil, nil)
	check("restricted download denied", status == 403, status)
	status, _, _ = bob.upload(urlB, vaultID, "restricted/new.md", []byte("x"))
	check("restricted upload denied", status == 403, status)

	status, _, _ = bob.do("GET", "/vaults/v1/"+vaultID+"/access", nil)
	check("editors cannot read access rules", status == 403, status)
	status, _, _ = bob.do("POST", "/vaults/v1/"+vaultID+"/groups", map[string]any{"name": "Nope"})
	check("editors cannot create groups", status == 403, status)

	status, body, _ = alice.do("POST", "/vaults/v1/"+vaultID+"/groups", map[string]any{"name": "Writers", "member_ids": []string{bob.userID}})
	group := jsonMap(body)
	groupID, _ := group["id"].(string)
	check("owner creates group", status == 201 && groupID != "" && len(group["member_ids"].([]any)) == 1, status, string(body))
	status, body, _ = alice.do("POST", "/vaults/v1/"+vaultID+"/groups", map[string]any{"name": "writers"})
	check("group names are unique per vault", status == 409 && jsonMap(body)["code"] == "group_name_taken", status, string(body))
	status, body, _ = bob.do("GET", "/vaults/v1/"+vaultID+"/groups", nil)
	check("members list groups", status == 200 && strings.Contains(string(body), "Writers"), status, string(body))

	status, _, _ = alice.do("PUT", "/vaults/v1/"+vaultID+"/access", map[string]any{"path": "restricted/", "restricted": true, "entries": []map[string]any{
		{"principal_type": "group", "principal_id": groupID, "level": "edit"},
	}})
	check("group granted edit on restricted folder", status == 200, status)
	status, body, _ = alice.do("PUT", "/vaults/v1/"+vaultID+"/access", map[string]any{"path": "restricted/sub/", "restricted": true, "entries": []any{}})
	check("nested restriction inside a granted folder", status == 200, status, string(body))
	ok = eventually(3*time.Second, func() bool {
		paths := listPaths(bob, urlB, vaultID)
		return contains(paths, "restricted/secret.md") && !contains(paths, "restricted/sub/deep.md")
	})
	check("group grant gives access, nested restriction still hides", ok, listPaths(bob, urlB, vaultID))
	status, _, _ = bob.upload(urlB, vaultID, "restricted/new.md", []byte("x"))
	check("group member writes in granted folder", status == 201, status)

	var skipped []map[string]string
	alice.upload(urlA, vaultID, "restricted/sub/leak.md", []byte("x"))
	alice.upload(urlA, vaultID, "restricted/visible.md", []byte("x"))
	got := bobSSE.waitMatch(5*time.Second, func(e map[string]string) bool { return strings.Contains(e["data"], "restricted/visible.md") }, &skipped)
	leaked := false
	for _, e := range skipped {
		leaked = leaked || strings.Contains(e["data"], "restricted/sub/")
	}
	check("SSE scope recomputed after access_changed without reconnect", got != nil && !leaked, got, skipped)

	status, _, _ = carol.upload(urlA, vaultID, "team/b.md", []byte("x"))
	check("viewer cannot write by default", status == 403, status)
	status, _, _ = alice.do("PUT", "/vaults/v1/"+vaultID+"/access", map[string]any{"path": "team/", "restricted": false, "entries": []map[string]any{
		{"principal_type": "user", "principal_id": carol.userID, "level": "edit"},
	}})
	check("viewer granted edit on folder", status == 200, status)
	ok = eventually(3*time.Second, func() bool {
		status, _, _ := carol.upload(urlB, vaultID, "team/b.md", []byte("x"))
		return status == 201
	})
	check("viewer with edit entry can write there", ok)
	status, _, _ = carol.upload(urlB, vaultID, "open2.md", []byte("x"))
	check("viewer still read-only elsewhere", status == 403, status)

	_, body, _ = carol.do("GET", "/vaults/v1/"+vaultID+"/overview", nil)
	overview := jsonMap(body)
	myAccess, _ := overview["my_access"].(map[string]any)
	check("viewer overview has compiled my_access and groups, no rules", myAccess["base"] == "view" && myAccess["all"] == false &&
		strings.Contains(string(body), `{"path":"team/","level":"edit"}`) && strings.Contains(string(body), `{"path":"restricted/","level":"none"}`) &&
		overview["access_rules"] == nil && len(overview["groups"].([]any)) == 1, string(body))
	_, body, _ = alice.do("GET", "/vaults/v1/"+vaultID+"/overview", nil)
	overview = jsonMap(body)
	myAccess, _ = overview["my_access"].(map[string]any)
	check("owner overview has access_rules and all access", myAccess["all"] == true && len(overview["access_rules"].([]any)) == 3, string(body))

	status, _, _ = alice.do("POST", base+"/files/rename", map[string]string{"old_path": "restricted/secret.md", "new_path": "archive/secret.md"})
	check("rename out of restricted folder", status == 200, status)
	_, body, _ = alice.do("GET", "/vaults/v1/"+vaultID+"/access", nil)
	check("folder restriction carried to the new folder", strings.Contains(string(body), `"path":"archive/","restricted":true`), string(body))
	ok = eventually(3*time.Second, func() bool {
		return !contains(listPaths(carol, urlB, vaultID), "archive/secret.md") && contains(listPaths(bob, urlB, vaultID), "archive/secret.md")
	})
	check("carried rule keeps the file restricted", ok)

	status, _, _ = alice.do("DELETE", "/vaults/v1/"+vaultID+"/groups/"+groupID, nil)
	check("owner deletes group", status == 204, status)
	ok = eventually(3*time.Second, func() bool { return !contains(listPaths(bob, urlB, vaultID), "restricted/visible.md") })
	check("deleting the group revokes its access", ok)

	ownerSSE := openSSE(alice, urlB, vaultID)
	status, _, _ = alice.do("DELETE", "/vaults/v1/"+vaultID+"/members/"+carol.userID, nil)
	check("owner removes viewer", status == 200, status)
	check("member removal publishes access_changed", ownerSSE.waitFor("access_changed", 5*time.Second) != nil)
	_, body, _ = alice.do("GET", "/vaults/v1/"+vaultID+"/access", nil)
	check("removing a member drops their entries", !strings.Contains(string(body), carol.userID) && !strings.Contains(string(body), `"path":"team/"`), string(body))
	status, _, _ = alice.do("PATCH", "/vaults/v1/"+vaultID+"/members/"+bob.userID, map[string]string{"role": "viewer"})
	check("role change", status == 200, status)
	check("role change publishes access_changed", ownerSSE.waitFor("access_changed", 5*time.Second) != nil)
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func b64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func authKey() string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(32))
}

func TestKeysPreloginAndReset(t *testing.T) {
	check := checker(t)

	unknown := uniqueEmail("ghost")
	_, body, _ := call(urlA, "POST", "/auth/v1/prelogin", map[string]string{"email": unknown}, nil)
	first := jsonMap(body)
	_, body, _ = call(urlB, "POST", "/auth/v1/prelogin", map[string]string{"email": strings.ToUpper(unknown)}, nil)
	second := jsonMap(body)
	kdf, _ := first["kdf"].(map[string]any)
	check("unknown email gets a deterministic fake salt on every instance", first["kdf_version"] == float64(1) && first["kdf_salt"] != nil &&
		first["kdf_salt"] == second["kdf_salt"] && kdf["algorithm"] == "argon2id" && kdf["memory_kib"] == float64(65536) &&
		kdf["iterations"] == float64(3) && kdf["parallelism"] == float64(1), first, second)

	email := uniqueEmail("kdf")
	salt, pub, wrapped, recovery := randomBytes(16), randomBytes(32), randomBytes(60), randomBytes(60)
	password := authKey()
	status, body, _ := call(urlA, "POST", "/auth/v1/register", map[string]string{
		"email": email, "password": password, "display_name": "Kdf", "locale": "en",
		"kdf_salt": b64(salt), "public_key": b64(pub), "encrypted_private_key": b64(wrapped), "recovery_wrapped_private_key": b64(recovery),
	}, nil)
	check("register with kdf fields", status == 201, status, string(body))
	_, body, _ = call(urlB, "POST", "/auth/v1/prelogin", map[string]string{"email": email}, nil)
	check("prelogin returns the stored salt", jsonMap(body)["kdf_version"] == float64(1) && jsonMap(body)["kdf_salt"] == b64(salt), string(body))
	user := login(urlB, email, password)
	check("login with derived auth key", user != nil)
	if user == nil {
		return
	}
	_, body, _ = call(urlA, "POST", "/auth/v1/login", map[string]string{"email": email, "password": password, "device_id": user.device, "device_name": "e2e", "device_type": "desktop"}, nil)
	lr := jsonMap(body)
	check("login payload carries kdf and wrapped identity", lr["kdf_version"] == float64(1) && lr["public_key"] == b64(pub) && lr["encrypted_private_key"] == b64(wrapped), string(body))
	_, body, _ = user.do("GET", "/account/v1/me/keys", nil)
	keys := jsonMap(body)
	check("keys endpoint", keys["kdf_version"] == float64(1) && keys["kdf_salt"] == b64(salt) && keys["has_recovery"] == true, string(body))

	status, _, _ = user.do("PUT", "/account/v1/me/password", map[string]string{"current_password": password, "new_password": authKey()})
	check("kdf user must send rewrapped key on password change", status == 400, status)
	newSalt, newWrapped, newPassword := randomBytes(16), randomBytes(60), authKey()
	status, _, _ = user.do("PUT", "/account/v1/me/password", map[string]string{
		"current_password": password, "new_password": newPassword, "kdf_salt": b64(newSalt), "encrypted_private_key": b64(newWrapped),
	})
	check("password change rewraps identity", status == 204, status)
	_, body, _ = call(urlA, "POST", "/auth/v1/prelogin", map[string]string{"email": email}, nil)
	check("prelogin reflects the new salt", jsonMap(body)["kdf_salt"] == b64(newSalt), string(body))
	password = newPassword

	legacy := newUser(t, urlA, "Legacykeys")
	_, body, _ = call(urlA, "POST", "/auth/v1/login", map[string]string{"email": legacy.email, "password": "password123", "device_id": legacy.device, "device_name": "e2e", "device_type": "desktop"}, nil)
	lr = jsonMap(body)
	_, hasPub := lr["public_key"]
	check("legacy login payload", lr["kdf_version"] == float64(0) && hasPub && lr["public_key"] == nil && lr["encrypted_private_key"] == nil, string(body))
	legacy.token, _ = lr["access_token"].(string)

	vaultID := createVault(t, user, "Keys")
	addMember(t, user, vaultID, legacy, "editor")
	_, body, _ = user.do("GET", "/vaults/v1/"+vaultID+"/keys/pending", nil)
	check("pending list carries identity details", strings.Contains(string(body), `"display_name":"Legacykeys"`) && strings.Contains(string(body), legacy.email), string(body))
	status, _, _ = user.do("PUT", "/vaults/v1/"+vaultID+"/keys/me", map[string]string{"encrypted_key": b64(randomBytes(93))})
	check("owner stores own sealed vault key", status == 200, status)
	status, _, _ = user.do("PUT", "/vaults/v1/"+vaultID+"/keys/"+legacy.userID, map[string]string{"encrypted_key": b64(randomBytes(93))})
	check("owner grants legacy member", status == 200, status)
	_, body, _ = user.do("GET", encryptionPath(vaultID), nil)
	check("encryption reports member keys", jsonMap(body)["has_member_keys"] == true, string(body))
	_, body, _ = user.do("GET", "/vaults/v1/"+vaultID+"/members/", nil)
	check("members expose public keys", strings.Contains(string(body), b64(pub)), string(body))

	ownerSSE := openSSE(user, urlB, vaultID)
	upgradeSalt, upgradePub, upgradeWrapped, upgradePassword := randomBytes(16), randomBytes(32), randomBytes(60), authKey()
	status, _, _ = legacy.do("PUT", "/account/v1/me/keys", map[string]string{
		"current_password": "wrong", "password": upgradePassword,
		"kdf_salt": b64(upgradeSalt), "public_key": b64(upgradePub), "encrypted_private_key": b64(upgradeWrapped),
	})
	check("key upgrade checks current password", status == 401, status)
	status, body, _ = legacy.do("PUT", "/account/v1/me/keys", map[string]string{
		"current_password": "password123", "password": upgradePassword,
		"kdf_salt": b64(upgradeSalt), "public_key": b64(upgradePub), "encrypted_private_key": b64(upgradeWrapped),
	})
	check("legacy account upgrades keys", status == 200 && jsonMap(body)["kdf_version"] == float64(1) && jsonMap(body)["has_recovery"] == false, status, string(body))
	keyChanged := ownerSSE.waitFor("member_key_changed", 5*time.Second)
	check("member_key_changed published across instances", keyChanged != nil && strings.Contains(keyChanged["data"], legacy.userID), keyChanged)
	status, _, _ = legacy.do("GET", "/vaults/v1/"+vaultID+"/keys/me", nil)
	check("stale vault key dropped after identity change", status == 404, status)
	status, _, _ = call(urlB, "POST", "/auth/v1/token/refresh", map[string]string{"refresh_token": legacy.refresh}, nil)
	check("key upgrade keeps sessions", status == 200, status)
	check("old password no longer works", login(urlA, legacy.email, "password123") == nil)
	check("derived password works", login(urlB, legacy.email, upgradePassword) != nil)
	status, body, _ = legacy.do("PUT", "/account/v1/me/recovery", map[string]string{"recovery_wrapped_private_key": b64(randomBytes(60))})
	check("recovery key stored", status == 200 && jsonMap(body)["has_recovery"] == true, status, string(body))

	status, _, _ = call(urlA, "POST", "/account/v1/password/forgot", map[string]string{"email": email}, nil)
	check("forgot password for kdf user", status == 202, status)
	mail, _ := waitEmail(t, email, "password_reset", 0)
	resetToken := tokenFrom(mail.Text, tokenRe)
	for i := 0; i < 2; i++ {
		status, body, _ = call(urlB, "POST", "/account/v1/password/reset/prepare", map[string]string{"token": resetToken}, nil)
		prepared := jsonMap(body)
		check("reset prepare returns identity without consuming the token", status == 200 && prepared["public_key"] == b64(pub) && prepared["recovery_wrapped_private_key"] == b64(recovery), status, string(body))
	}
	status, _, _ = call(urlA, "POST", "/account/v1/password/reset/prepare", map[string]string{"token": "bogus"}, nil)
	check("reset prepare rejects unknown tokens", status == 404, status)
	resetSalt, resetWrapped, resetPassword := randomBytes(16), randomBytes(60), authKey()
	status, body, _ = call(urlA, "POST", "/account/v1/password/reset", map[string]string{
		"token": resetToken, "password": resetPassword,
		"kdf_salt": b64(resetSalt), "public_key": b64(pub), "encrypted_private_key": b64(resetWrapped),
	}, nil)
	check("reset with kdf fields", status == 200, status, string(body))
	check("login with reset auth key", login(urlB, email, resetPassword) != nil)
	check("previous password rejected", login(urlB, email, password) == nil)
	_, body, _ = call(urlA, "POST", "/auth/v1/prelogin", map[string]string{"email": email}, nil)
	check("prelogin returns the reset salt", jsonMap(body)["kdf_salt"] == b64(resetSalt), string(body))
	owner := login(urlA, email, resetPassword)
	status, _, _ = owner.do("GET", "/vaults/v1/"+vaultID+"/keys/me", nil)
	check("reset with the same identity keeps vault keys", status == 200, status)
}

func encryptionPath(vaultID string) string {
	return "/sync/v1/vaults/" + vaultID + "/encryption"
}
