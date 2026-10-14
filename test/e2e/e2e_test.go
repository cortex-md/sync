//go:build e2e

package e2e

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cortexnotes/cortex-sync/internal/platform/config"
	"github.com/cortexnotes/cortex-sync/internal/platform/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func checker(t *testing.T) func(name string, ok bool, detail ...any) {
	return func(name string, ok bool, detail ...any) {
		t.Helper()
		if !ok {
			t.Errorf("%s: %s", name, fmt.Sprint(detail...))
		}
	}
}

func TestMultiInstance(t *testing.T) {
	check := checker(t)
	A, B := urlA, urlB
	run := runID

	aliceEmail := "alice" + run + "@example.com"
	bobEmail := "bob" + run + "@example.com"
	carolEmail := "carol" + run + "@example.com"

	register(t, A, strings.ToUpper(aliceEmail[:1])+aliceEmail[1:], "Alice", "pt-BR")
	register(t, B, bobEmail, "Bob", "en")
	register(t, A, carolEmail, "Carol", "en")
	status, _, _ := call(A, "POST", "/auth/v1/register", map[string]string{"email": "bad@", "password": "password123", "display_name": "x"}, nil)
	check("register rejects invalid email", status == 400, status)

	alice := login(A, aliceEmail, "password123")
	alice2 := login(B, aliceEmail, "password123")
	bob := login(B, bobEmail, "password123")
	carol := login(A, carolEmail, "password123")
	check("logins", alice != nil && alice2 != nil && bob != nil && carol != nil)

	mail, _ := waitEmail(t, aliceEmail, "verify_email", 0)
	check("verification email queued in user locale", strings.Contains(mail.Subject, "Confirme"), mail.Subject)
	verifyToken := tokenFrom(mail.Text, tokenRe)
	status, page, _ := call(A, "GET", "/account/v1/email/verify?token="+url.QueryEscape(verifyToken), nil, map[string]string{"Accept-Language": "pt-BR"})
	check("verify page renders form without consuming token", status == 200 && bytes.Contains(page, []byte(`method="post"`)), status)
	status, _, _ = call(B, "POST", "/account/v1/email/verify", map[string]string{"token": verifyToken}, nil)
	check("verify email via other instance", status == 200, status)
	status, _, _ = call(A, "POST", "/account/v1/email/verify", map[string]string{"token": verifyToken}, nil)
	check("verification token is single use", status == 404, status)
	_, body, _ := alice.do("GET", "/account/v1/me/", nil)
	me := jsonMap(body)
	check("me shows verified + locale", me["email_verified"] == true && me["locale"] == "pt-BR", string(body))

	status, body, _ = alice.do("PATCH", "/account/v1/me/", map[string]string{"display_name": "Alice A."})
	check("update display name", status == 200 && jsonMap(body)["display_name"] == "Alice A.", status, string(body))
	status, _, _ = alice.do("PATCH", "/account/v1/me/", map[string]string{"display_name": "  "})
	check("blank display name rejected", status == 400, status)

	status, body, _ = alice.doOn(A, "PUT", "/account/v1/me/avatar", pngBytes(), map[string]string{"Content-Type": "image/png"})
	avatarURL, _ := jsonMap(body)["avatar_url"].(string)
	check("avatar upload", status == 200 && strings.HasPrefix(avatarURL, "/avatars/v1/"), status, string(body))
	status, img, headers := call(B, "GET", avatarURL, nil, nil)
	check("avatar served by other instance as cached jpeg", status == 200 && headers.Get("Content-Type") == "image/jpeg" && strings.Contains(headers.Get("Cache-Control"), "immutable") && len(img) > 0, status)
	status, _, _ = call(A, "GET", avatarURL, nil, map[string]string{"If-None-Match": headers.Get("ETag")})
	check("avatar conditional request 304", status == 304, status)
	status, _, _ = alice.doOn(A, "PUT", "/account/v1/me/avatar", []byte("hello"), nil)
	check("non-image avatar rejected", status == 400, status)

	alicePub := make([]byte, 32)
	alicePub[0] = 7
	status, _, _ = alice.do("PUT", "/account/v1/me/public-key", map[string]string{"public_key": base64.StdEncoding.EncodeToString(alicePub)})
	check("set public key", status == 204, status)
	status, body, _ = bob.do("GET", "/users/v1/"+alice.userID+"/public-key", nil)
	check("read peer public key", status == 200 && jsonMap(body)["public_key"] == base64.StdEncoding.EncodeToString(alicePub), status, string(body))
	status, _, _ = alice.do("PUT", "/account/v1/me/public-key", map[string]string{"public_key": base64.StdEncoding.EncodeToString([]byte("short"))})
	check("invalid public key rejected", status == 400, status)

	status, _, _ = alice.do("PUT", "/account/v1/me/password", map[string]string{"current_password": "wrong-pass", "new_password": "newpassword1"})
	check("change password with wrong current -> 401", status == 401, status)
	status, _, _ = alice.do("PUT", "/account/v1/me/password", map[string]string{"current_password": "password123", "new_password": "newpassword1"})
	check("change password", status == 204, status)
	status, _, _ = call(B, "POST", "/auth/v1/token/refresh", map[string]string{"refresh_token": alice2.refresh}, nil)
	check("other device session revoked after password change", status == 401, status)
	status, body, _ = call(A, "POST", "/auth/v1/token/refresh", map[string]string{"refresh_token": alice.refresh}, nil)
	check("current device session kept after password change", status == 200, status)
	alice.refresh, _ = jsonMap(body)["refresh_token"].(string)
	mail, _ = waitEmail(t, aliceEmail, "password_changed", 0)
	check("password changed notice", mail.Subject != "", mail)

	newAliceEmail := "alice-new" + run + "@example.com"
	status, _, _ = alice.do("POST", "/account/v1/me/email", map[string]string{"password": "newpassword1", "new_email": bobEmail})
	check("email change to taken address -> 409", status == 409, status)
	status, _, _ = alice.do("POST", "/account/v1/me/email", map[string]string{"password": "newpassword1", "new_email": newAliceEmail})
	check("request email change", status == 202, status)
	notice, _ := waitEmail(t, aliceEmail, "email_changed", 0)
	check("old address notified of email change", strings.Contains(notice.Text, newAliceEmail), notice.Text)
	mail, _ = waitEmail(t, newAliceEmail, "email_change", 0)
	changeToken := tokenFrom(mail.Text, tokenRe)
	status, page, _ = call(B, "POST", "/account/v1/email/confirm-change", url.Values{"token": {changeToken}}, nil)
	check("confirm email change via html form", status == 200 && bytes.Contains(page, []byte("<html")), status)
	check("login with new email", login(A, newAliceEmail, "newpassword1") != nil)
	check("old email no longer logs in", login(A, aliceEmail, "newpassword1") == nil)

	status, _, _ = call(A, "POST", "/account/v1/password/forgot", map[string]string{"email": "nobody" + run + "@example.com"}, nil)
	check("forgot password does not reveal unknown accounts", status == 202, status)
	status, _, _ = call(A, "POST", "/account/v1/password/forgot", map[string]string{"email": strings.ToUpper(bobEmail)}, nil)
	check("forgot password", status == 202, status)
	mail, _ = waitEmail(t, bobEmail, "password_reset", 0)
	resetToken := tokenFrom(mail.Text, tokenRe)
	status, page, _ = call(B, "GET", "/account/v1/password/reset?token="+url.QueryEscape(resetToken), nil, nil)
	check("reset page links to the app instead of posting a password", status == 200 &&
		bytes.Contains(page, []byte("cortex://reset-password?token="+url.QueryEscape(resetToken)+"&amp;server="+url.QueryEscape(B))) &&
		!bytes.Contains(page, []byte(`type="password"`)), status, string(page))
	status, _, _ = call(A, "POST", "/account/v1/password/reset", url.Values{"token": {resetToken}, "password": {"short"}}, nil)
	check("reset rejects short password without consuming token", status == 400, status)
	status, _, _ = call(A, "POST", "/account/v1/password/reset", url.Values{"token": {resetToken}, "password": {"bobpassword9"}}, nil)
	check("reset password", status == 200, status)
	status, _, _ = call(A, "POST", "/account/v1/password/reset", url.Values{"token": {resetToken}, "password": {"bobpassword9"}}, nil)
	check("reset token single use", status == 410, status)
	status, _, _ = call(B, "POST", "/auth/v1/token/refresh", map[string]string{"refresh_token": bob.refresh}, nil)
	check("password reset revokes all sessions", status == 401, status)
	bob = login(B, bobEmail, "bobpassword9")
	check("login with reset password", bob != nil)

	status, body, _ = alice.do("POST", "/vaults/v1/", map[string]string{"name": "Team " + run})
	vaultID, _ := jsonMap(body)["id"].(string)
	check("create vault", status == 201, status, string(body))
	aliceSSE := openSSE(alice, A, vaultID)

	status, body, _ = alice.do("POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{"invitee_email": bobEmail, "role": "editor"})
	inv := jsonMap(body)
	inviteToken, _ := inv["invite_token"].(string)
	check("invite without vault key returns link", status == 201 && inviteToken != "" && strings.HasSuffix(inv["invite_url"].(string), "/invite/"+inviteToken), status, string(body))
	mail, _ = waitEmail(t, bobEmail, "vault_invite", 0)
	check("invite email carries the link", tokenFrom(mail.Text, inviteRe) == inviteToken, mail.Text)
	status, body, _ = call(B, "GET", "/invites/v1/"+inviteToken, nil, nil)
	check("public invite preview", status == 200 && jsonMap(body)["inviter_name"] == "Alice A.", status, string(body))
	status, page, _ = call(A, "GET", "/invite/"+inviteToken, nil, nil)
	check("invite landing deep link", status == 200 && bytes.Contains(page, []byte("cortex://invite/"+inviteToken)), status)

	status, body, _ = bob.do("POST", "/vaults/v1/invites/accept", map[string]string{"token": inviteToken})
	check("accept invite by token on other instance", status == 200 && jsonMap(body)["id"] == vaultID, status, string(body))
	joined := aliceSSE.waitFor("member_joined", 3*time.Second)
	check("owner notified of member_joined across instances", joined != nil && strings.Contains(joined["data"], bob.userID), joined)

	status, body, _ = alice.do("GET", "/vaults/v1/"+vaultID+"/keys/pending", nil)
	check("pending key list includes new member", status == 200 && strings.Contains(string(body), bob.userID), status, string(body))
	bobPub := make([]byte, 32)
	bobPub[0] = 9
	bob.do("PUT", "/account/v1/me/public-key", map[string]string{"public_key": base64.StdEncoding.EncodeToString(bobPub)})
	_, body, _ = alice.do("GET", "/vaults/v1/"+vaultID+"/keys/pending", nil)
	check("pending list exposes member public key", strings.Contains(string(body), base64.StdEncoding.EncodeToString(bobPub)), string(body))
	wrapped := base64.StdEncoding.EncodeToString([]byte("vek-wrapped-for-bob"))
	status, _, _ = bob.do("PUT", "/vaults/v1/"+vaultID+"/keys/"+alice.userID, map[string]string{"encrypted_key": wrapped})
	check("editor cannot grant keys", status == 403, status)
	status, _, _ = alice.do("PUT", "/vaults/v1/"+vaultID+"/keys/"+bob.userID, map[string]string{"encrypted_key": wrapped})
	check("owner grants key", status == 200, status)
	status, body, _ = bob.do("GET", "/vaults/v1/"+vaultID+"/keys/me", nil)
	check("member reads granted key", status == 200 && jsonMap(body)["encrypted_key"] == wrapped, status, string(body))
	_, body, _ = alice.do("GET", "/vaults/v1/"+vaultID+"/members/", nil)
	check("members list has avatar and key status", strings.Contains(string(body), avatarURL) && strings.Contains(string(body), `"has_key":true`), string(body))

	status, _, _ = bob.do("POST", "/sync/v1/vaults/"+vaultID+"/encryption", map[string]string{"salt": "c2FsdA==", "encrypted_vek": "dmVr"})
	check("editor cannot overwrite vault encryption", status == 403, status)
	status, _, _ = bob.do("POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{"invitee_email": "x" + run + "@example.com", "role": "viewer"})
	check("editor cannot invite", status == 403, status)

	status, body, _ = alice.do("POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{"invitee_email": carolEmail, "role": "admin"})
	carolInviteID, _ := jsonMap(body)["id"].(string)
	check("owner invites admin", status == 201, status, string(body))
	status, _, _ = carol.do("POST", "/vaults/v1/invites/accept", map[string]string{"invite_id": carolInviteID})
	check("accept invite by id (email match)", status == 200, status)
	status, _, _ = carol.do("PATCH", "/vaults/v1/"+vaultID+"/members/"+bob.userID, map[string]string{"role": "admin"})
	check("admin cannot promote to admin", status == 403, status)

	bobSSE := openSSE(bob, B, vaultID)
	status, _, _ = carol.do("PATCH", "/vaults/v1/"+vaultID+"/members/"+bob.userID, map[string]string{"role": "viewer"})
	check("admin demotes editor to viewer", status == 200, status)
	check("role change disconnects member SSE on other instance", bobSSE.closedWithin(3*time.Second))
	status, _, _ = bob.doOn(B, "POST", "/sync/v1/vaults/"+vaultID+"/files", []byte("x"), map[string]string{"X-File-Path": "a.md"})
	check("viewer cannot upload", status == 403, status)

	file := "notes/shared.md"
	aliceWS, err := dialCollab(alice, B, A, vaultID, file, "")
	check("ticket issued on B accepted by A", err == nil, err)
	welcome := readUntil(aliceWS, "welcome", 3*time.Second)
	check("welcome with identity", welcome["from"] != nil && welcome["client_id"] != nil, welcome)
	active := aliceSSE.waitFor("collab_active", 3*time.Second)
	check("collab_active SSE", active != nil && strings.Contains(active["data"], file), active)

	carolWS, err := dialCollab(carol, A, B, vaultID, file, "")
	check("second editor joins on other instance", err == nil, err)
	carolWelcome := readUntil(carolWS, "welcome", 3*time.Second)
	peers, _ := carolWelcome["peers"].([]any)
	check("welcome lists remote peer", len(peers) == 1, carolWelcome)
	joinedPeer := readUntil(aliceWS, "peer_joined", 3*time.Second)
	check("peer_joined relayed across instances", joinedPeer["peer"] != nil, joinedPeer)

	writeJSON(carolWS, map[string]any{"type": "update", "data": []byte("yjs-update")})
	upd := readUntil(aliceWS, "update", 3*time.Second)
	from, _ := upd["from"].(map[string]any)
	check("update relayed across instances with stamped sender", from != nil && from["user_id"] == carol.userID, upd)

	big := bytes.Repeat([]byte("a"), 20000)
	writeJSON(carolWS, map[string]any{"type": "update", "data": big})
	bigUpd := readUntil(aliceWS, "update", 3*time.Second)
	gotBig, _ := base64.StdEncoding.DecodeString(fmt.Sprint(bigUpd["data"]))
	check("large update relayed through relay table (>8KB NOTIFY limit)", len(gotBig) == len(big), len(gotBig))

	writeJSON(aliceWS, map[string]any{"type": "awareness", "data": []byte("cursor:10"), "from": map[string]any{"user_id": carol.userID}})
	aw := readUntil(carolWS, "awareness", 3*time.Second)
	awFrom, _ := aw["from"].(map[string]any)
	check("awareness (live cursor) relayed with unspoofable identity", awFrom != nil && awFrom["user_id"] == alice.userID && awFrom["avatar_url"] == avatarURL, aw)

	status, body, _ = bob.do("GET", "/sync/v1/vaults/"+vaultID+"/collab/peers?path="+url.QueryEscape(file), nil)
	check("peers endpoint sees both instances", status == 200 && jsonMap(body)["peer_count"] == float64(2), status, string(body))
	status, body, _ = bob.do("GET", "/sync/v1/vaults/"+vaultID+"/presence", nil)
	check("vault presence groups by file", status == 200 && strings.Contains(string(body), file) && strings.Contains(string(body), "Carol"), status, string(body))

	time.Sleep(1500 * time.Millisecond)
	status, _, _ = alice.do("DELETE", "/vaults/v1/"+vaultID+"/members/"+carol.userID, nil)
	check("owner removes admin", status == 204, status)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, _, readErr := carolWS.Read(ctx)
	cancel()
	check("removed member's collab socket closed with 4403", websocket.CloseStatus(readErr) == 4403, readErr)
	left := readUntil(aliceWS, "peer_left", 3*time.Second)
	check("peer_left relayed", left["client_id"] != nil, left)

	aliceWS.Close(websocket.StatusNormalClosure, "")
	time.Sleep(500 * time.Millisecond)
	rejoin, _ := dialCollab(alice, A, A, vaultID, file, "")
	rw := readUntil(rejoin, "welcome", 3*time.Second)
	upTo, _ := rw["up_to"].(float64)
	check("updates persisted after flush (welcome.up_to > 0)", upTo > 0, rw["up_to"])
	writeJSON(rejoin, map[string]any{"type": "compact", "data": []byte("state"), "up_to": upTo})
	time.Sleep(300 * time.Millisecond)
	rejoin.Close(websocket.StatusNormalClosure, "")
	time.Sleep(300 * time.Millisecond)
	again, _ := dialCollab(alice, A, B, vaultID, file, "")
	ag := readUntil(again, "welcome", 3*time.Second)
	check("compaction removed persisted updates", ag["up_to"] == nil || ag["up_to"] == float64(0), ag["up_to"])
	state := readUntil(again, "sync_step2", 3*time.Second)
	check("compacted state served on join", state["data"] == base64.StdEncoding.EncodeToString([]byte("state")), state)
	synced := readUntil(again, "synced", 3*time.Second)
	check("synced closes the initial backlog", synced["type"] == "synced", synced)
	again.Close(websocket.StatusNormalClosure, "")

	status, _, _ = alice.upload(A, vaultID, file, []byte("note"))
	check("upload collab note", status == 200 || status == 201, status)
	status, _, _ = alice.doOn(A, "DELETE", "/sync/v1/vaults/"+vaultID+"/files?path="+url.QueryEscape(file), nil, nil)
	check("delete collab note", status == 204, status)
	afterDelete, _ := dialCollab(alice, A, A, vaultID, file, "")
	backlog := readTypesUntil(afterDelete, "synced", 3*time.Second)
	afterDelete.Close(websocket.StatusNormalClosure, "")
	check("file delete removes collab document", strings.Join(backlog, ",") == "welcome,synced", backlog)

}

func TestSyncContracts(t *testing.T) {
	check := checker(t)
	alice := newUser(t, urlA, "Synca")
	vaultID := createVault(t, alice, "Sync")
	base := "/sync/v1/vaults/" + vaultID

	_, _, headers := alice.do("GET", base+"/files/list", nil)
	etag := headers.Get("ETag")
	status, _, _ := alice.doOn(urlB, "GET", base+"/files/list", nil, map[string]string{"If-None-Match": etag})
	check("unchanged list answers 304 on any instance", status == 304 && etag != "", status, etag)

	const total = 200
	contents := make(map[string][]byte, total)
	paths := make([]string, 0, total)
	for i := range total {
		path := fmt.Sprintf("bulk/%03d note ç.md", i)
		paths = append(paths, path)
		contents[path] = bytes.Repeat([]byte(fmt.Sprintf("%s-%d|", runID, i)), 50+i)
	}
	failures := make(chan string, total)
	work := make(chan string)
	done := make(chan struct{})
	for range 8 {
		go func() {
			for path := range work {
				if status, body, _ := alice.upload(urlA, vaultID, path, contents[path]); status != 201 {
					failures <- fmt.Sprintf("%s: %d %s", path, status, body)
				}
			}
			done <- struct{}{}
		}()
	}
	for _, p := range paths {
		work <- p
	}
	close(work)
	for range 8 {
		<-done
	}
	close(failures)
	for f := range failures {
		t.Errorf("upload %s", f)
	}

	status, _, headers = alice.do("GET", base+"/files/list", nil)
	check("list changes after uploads", status == 200 && headers.Get("ETag") != etag, status)
	latest := headers.Get("X-Latest-Event-ID")
	_, body, _ := alice.do("GET", base+"/changes?since=0&limit=1000", nil)
	changes := jsonList(body)
	check("X-Latest-Event-ID is the newest event", len(changes) == total && fmt.Sprint(changes[len(changes)-1]["id"]) == latest, latest, len(changes))

	seen := map[string]bool{}
	cursor, pages := "", 0
	for {
		q := "?limit=64"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		status, body, headers := alice.doOn(urlB, "GET", base+"/files/list"+q, nil, nil)
		if status != 200 {
			t.Fatalf("page: %d %s", status, body)
		}
		pages++
		for _, f := range jsonList(body) {
			seen[f["file_path"].(string)] = true
			if f["created_by"] != alice.userID {
				t.Errorf("created_by missing on %v", f["file_path"])
			}
		}
		if cursor = headers.Get("X-Next-Cursor"); cursor == "" {
			break
		}
	}
	check("keyset pagination returns every file once", len(seen) == total && pages == 4, len(seen), pages)

	items := make([]map[string]any, 0, total)
	for _, p := range paths {
		items = append(items, map[string]any{"path": p, "version": 0})
	}
	status, body, headers = alice.doOn(urlB, "POST", base+"/files/batch", map[string]any{"items": items}, nil)
	frames, err := readBatch(body)
	check("batch download of 200 files", status == 200 && err == nil && len(frames) == total && headers.Get("Content-Type") == "application/vnd.cortex.batch", status, err, len(frames))
	intact := true
	for i, f := range frames {
		if f.Header["path"] != paths[i] || f.Header["status"] != "ok" || !bytes.Equal(f.Body, contents[paths[i]]) {
			intact = false
			t.Errorf("frame %d: %v", i, f.Header)
			break
		}
	}
	check("batch frames keep request order and content", intact)
	status, _, _ = alice.do("POST", base+"/files/batch", map[string]any{"items": append(items, map[string]any{"path": "x.md"})})
	check("batch rejects more than 200 items", status == 400, status)

	alice.upload(urlA, vaultID, paths[0], []byte("second version"))
	_, body, _ = alice.do("POST", base+"/files/batch", map[string]any{"items": []map[string]any{{"path": paths[0], "version": 1}, {"path": paths[0], "version": 2}, {"path": "missing.md"}}})
	frames, _ = readBatch(body)
	check("batch serves two versions for a diff", len(frames) == 3 && bytes.Equal(frames[0].Body, contents[paths[0]]) && string(frames[1].Body) == "second version" && frames[2].Header["status"] == "not_found", len(frames))

	status, _, _ = alice.do("PUT", base+"/cursor", map[string]any{"last_event_id": 42})
	check("per-vault cursor stored", status == 204, status)
	_, body, _ = alice.doOn(urlB, "GET", "/account/v1/overview", nil, nil)
	overview := jsonMap(body)
	check("account overview aggregates profile, vaults and devices", overview["account"] != nil && len(overview["vaults"].([]any)) == 1 && len(overview["devices"].([]any)) == 1, string(body))
	device := overview["devices"].([]any)[0].(map[string]any)
	cursors, _ := device["vault_cursors"].([]any)
	check("device last sync per vault", len(cursors) == 1 && cursors[0].(map[string]any)["last_event_id"] == float64(42), device)
	vaultSummary := overview["vaults"].([]any)[0].(map[string]any)
	check("vault summary carries usage and role", vaultSummary["role"] == "owner" && vaultSummary["storage_used_bytes"].(float64) > 0, vaultSummary)

	clientID := uuid.NewString()
	first := alice.attempt("POST", "/vaults/v1/", map[string]string{"id": clientID, "name": "Retry " + runID})
	second := alice.attempt("POST", "/vaults/v1/", map[string]string{"id": clientID, "name": "Retry " + runID})
	check("vault create is idempotent by client id", first.status == 201 && second.status < 300 && jsonMap(second.body)["id"] == clientID, first.status, second.status)
	_, body, _ = alice.do("GET", "/vaults/v1/", nil)
	check("retried create does not duplicate the vault", strings.Count(string(body), clientID) == 1, string(body))

	inviteID := uuid.NewString()
	invitee := uniqueEmail("retry-invitee")
	first = alice.attempt("POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{"id": inviteID, "invitee_email": invitee, "role": "viewer"})
	second = alice.attempt("POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{"id": inviteID, "invitee_email": invitee, "role": "viewer"})
	oldToken, _ := jsonMap(first.body)["invite_token"].(string)
	newToken, _ := jsonMap(second.body)["invite_token"].(string)
	check("invite create is idempotent and rotates the link", first.status == 201 && second.status < 300 && jsonMap(second.body)["id"] == inviteID && newToken != "" && newToken != oldToken, first.status, second.status)
	status, _, _ = call(urlB, "GET", "/invites/v1/"+oldToken, nil, nil)
	check("rotated invite token no longer resolves", status == 404, status)
	_, body, _ = alice.do("GET", "/vaults/v1/"+vaultID+"/invites/", nil)
	check("retried invite is stored once", strings.Count(string(body), inviteID) == 1, string(body))

	_, body, _ = alice.do("GET", "/vaults/v1/"+vaultID+"/overview", nil)
	vo := jsonMap(body)
	vaultInfo, _ := vo["vault"].(map[string]any)
	check("vault detail reports member count", vaultInfo["member_count"] == float64(1), vaultInfo)
	check("vault overview aggregates members, invites and devices", vo["my_role"] == "owner" && len(vo["members"].([]any)) == 1 && len(vo["invites"].([]any)) == 1 && len(vo["devices"].([]any)) == 1, string(body))
}

func TestGuestScope(t *testing.T) {
	check := checker(t)
	alice := newUser(t, urlA, "Guestowner")
	dave := newUser(t, urlB, "Guest")
	vaultID := createVault(t, alice, "Guests")
	base := "/sync/v1/vaults/" + vaultID
	for _, p := range []string{"public/a.md", "public/sub/b.md", "private/c.md", "notes/exact.md", "notes/exact.md.bak"} {
		alice.upload(urlA, vaultID, p, []byte(p))
	}

	status, body, _ := alice.do("POST", "/vaults/v1/"+vaultID+"/invites/", map[string]any{
		"invitee_email": dave.email, "role": "guest",
		"grants": []map[string]any{{"path": "public/", "can_write": true}, {"path": "notes/exact.md"}},
	})
	token, _ := jsonMap(body)["invite_token"].(string)
	check("guest invite with path grants", status == 201 && token != "", status, string(body))
	status, _, _ = dave.do("POST", "/vaults/v1/invites/accept", map[string]string{"token": token})
	check("guest accepts on other instance", status == 200, status)

	_, body, _ = dave.do("GET", base+"/files/list", nil)
	var visible []string
	for _, f := range jsonList(body) {
		visible = append(visible, f["file_path"].(string))
	}
	check("guest lists only granted folder and note", strings.Join(visible, ",") == "notes/exact.md,public/a.md,public/sub/b.md", visible)
	status, _, _ = dave.do("GET", base+"/files?path=private/c.md", nil)
	check("guest cannot download outside grants", status == 403, status)
	status, _, _ = dave.upload(urlB, vaultID, "public/new.md", []byte("x"))
	check("guest writes inside writable grant", status == 201, status)
	status, _, _ = dave.upload(urlB, vaultID, "notes/exact.md", []byte("x"))
	check("guest cannot write read-only grant", status == 403, status)
	status, _, _ = dave.upload(urlB, vaultID, "private/x.md", []byte("x"))
	check("guest cannot write outside grants", status == 403, status)

	_, body, _ = dave.do("POST", base+"/files/batch", map[string]any{"items": []map[string]any{{"path": "public/a.md"}, {"path": "private/c.md"}}})
	frames, _ := readBatch(body)
	check("batch enforces guest scope per item", len(frames) == 2 && frames[0].Header["status"] == "ok" && frames[1].Header["status"] == "forbidden" && len(frames[1].Body) == 0, frames)
	_, body, _ = dave.do("GET", base+"/changes?since=0", nil)
	check("change feed hides paths outside grants", !strings.Contains(string(body), "private/") && !strings.Contains(string(body), "exact.md.bak") && strings.Contains(string(body), "public/a.md"), string(body))

	sse := openSSE(dave, urlA, vaultID)
	alice.upload(urlB, vaultID, "private/d.md", []byte("d"))
	alice.upload(urlB, vaultID, "public/e.md", []byte("e"))
	var skipped []map[string]string
	got := sse.waitMatch(5*time.Second, func(e map[string]string) bool { return strings.Contains(e["data"], "public/e.md") }, &skipped)
	leaked := false
	for _, e := range skipped {
		leaked = leaked || strings.Contains(e["data"], "private/")
	}
	check("SSE delivers only granted paths across instances", got != nil && !leaked, got, skipped)

	_, err := dialCollab(dave, urlA, urlA, vaultID, "private/c.md", "")
	check("guest cannot open collab outside grants", err != nil && strings.Contains(err.Error(), "403"), err)

	_, body, _ = dave.do("GET", "/vaults/v1/"+vaultID+"/overview", nil)
	vo := jsonMap(body)
	myAccess, _ := vo["my_access"].(map[string]any)
	myRules, _ := myAccess["rules"].([]any)
	_, hasGrants := vo["my_grants"]
	check("guest overview shows compiled access only", vo["my_role"] == "guest" && myAccess["all"] == false && myAccess["base"] == "none" &&
		len(myRules) == 2 && !hasGrants && len(vo["members"].([]any)) == 0 && vo["access_rules"] == nil, string(body))
	status, _, _ = dave.do("GET", "/vaults/v1/"+vaultID+"/guests/"+dave.userID+"/grants/", nil)
	check("guest grant endpoints are gone", status == 404 || status == 405, status)

	_, _, headers := dave.do("GET", base+"/files/list", nil)
	guestETag := headers.Get("ETag")
	status, _, _ = alice.doOn(urlA, "PUT", "/vaults/v1/"+vaultID+"/access", map[string]any{"path": "public/", "restricted": false, "entries": []any{}}, nil)
	check("owner removes the guest folder entry", status == 204, status)
	ok := eventually(3*time.Second, func() bool {
		_, body, _ := dave.doOn(urlB, "GET", base+"/files/list", nil, nil)
		l := jsonList(body)
		return len(l) == 1 && l[0]["file_path"] == "notes/exact.md"
	})
	check("revoked entry takes effect on the other instance", ok)
	status, _, _ = dave.doOn(urlB, "GET", base+"/files/list", nil, map[string]string{"If-None-Match": guestETag})
	check("changed access invalidates the guest list ETag", status == 200, status)

	status, _, _ = alice.do("PATCH", "/vaults/v1/"+vaultID+"/members/"+dave.userID, map[string]string{"role": "viewer"})
	check("guest promoted to viewer", status == 200, status)
	_, body, _ = alice.do("GET", "/vaults/v1/"+vaultID+"/overview", nil)
	check("promotion keeps the member's access entries", strings.Contains(string(body), `"principal_id":"`+dave.userID+`"`), string(body))
	_, body, _ = dave.do("GET", base+"/files/list", nil)
	check("promoted member sees the whole vault", len(jsonList(body)) == 8, string(body))

	erin := newUser(t, urlA, "Lateguest")
	_, body, _ = alice.do("POST", "/vaults/v1/"+vaultID+"/invites/", map[string]any{"invitee_email": erin.email, "role": "guest", "grants": []map[string]any{{"path": "public/"}}})
	lateToken, _ := jsonMap(body)["invite_token"].(string)
	alice.do("PATCH", "/vaults/v1/"+vaultID, map[string]any{"allow_guests": false})
	status, body, _ = erin.do("POST", "/vaults/v1/invites/accept", map[string]string{"token": lateToken})
	check("guest invite cannot be accepted after guests are turned off", status == 404 && jsonMap(body)["code"] == "feature_disabled", status, string(body))
}

func TestOrphanedUploadIsCollected(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	blobs, err := s3.NewBlobStorage(ctx, config.S3Config{
		Backend:   "local",
		Endpoint:  env("CORTEX_E2E_S3_ENDPOINT", "127.0.0.1:9000"),
		AccessKey: env("CORTEX_E2E_S3_ACCESS_KEY", "minioadmin"),
		SecretKey: env("CORTEX_E2E_S3_SECRET_KEY", "minioadmin"),
		Bucket:    env("CORTEX_E2E_S3_BUCKET", "cortex-snapshots"),
		Region:    "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	key := "e2e-orphans/" + uuid.NewString()
	if _, err := blobs.Upload(ctx, key, strings.NewReader("orphan"), 6, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO blob_uploads_pending (blob_key, created_at) VALUES ($1, now() - interval '2 hours')`, key); err != nil {
		t.Fatal(err)
	}

	collected := eventually(90*time.Second, func() bool {
		exists, err := blobs.Exists(ctx, key)
		return err == nil && !exists
	})
	if !collected {
		t.Fatal("blob left behind by a crashed upload was not deleted")
	}
	var queued int
	_ = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM blob_uploads_pending WHERE blob_key = $1) + (SELECT count(*) FROM blob_deletions WHERE blob_key = $1)`, key).Scan(&queued)
	if queued != 0 {
		t.Fatalf("queues still reference the collected blob: %d", queued)
	}
}

func TestMinimalFeatureSet(t *testing.T) {
	if urlMinimal == "" {
		t.Skip("CORTEX_E2E_URL_MINIMAL not set")
	}
	check := checker(t)

	_, body, _ := call(urlMinimal, "GET", "/meta/v1/capabilities", nil, nil)
	caps := jsonMap(body)
	features, _ := caps["features"].(map[string]any)
	check("capabilities reflect disabled features", caps["tenancy_mode"] == "multi" && features["email"] == false && features["collab"] == false && features["avatars"] == false && features["guests"] == false && features["password_reset"] == false, string(body))

	alice := newUser(t, urlMinimal, "Minimal")
	disabled := func(status int, body []byte) bool {
		return status == 404 && jsonMap(body)["code"] == "feature_disabled"
	}
	status, body, _ := call(urlMinimal, "POST", "/account/v1/password/forgot", map[string]string{"email": alice.email}, nil)
	check("password reset disabled without mail", disabled(status, body), status, string(body))
	status, body, _ = alice.doOn(urlMinimal, "PUT", "/account/v1/me/avatar", pngBytes(), map[string]string{"Content-Type": "image/png"})
	check("avatars disabled", disabled(status, body), status, string(body))

	vaultID := createVault(t, alice, "Minimal")
	status, body, _ = alice.do("POST", "/sync/v1/vaults/"+vaultID+"/collab/tickets", map[string]string{"file_path": "a.md"})
	check("collab disabled", disabled(status, body), status, string(body))
	status, body, _ = alice.do("POST", "/vaults/v1/"+vaultID+"/invites/", map[string]any{"invitee_email": uniqueEmail("g"), "role": "guest", "grants": []map[string]any{{"path": "a/"}}})
	check("guest invites disabled", disabled(status, body), status, string(body))

	invitee := uniqueEmail("link-only")
	status, body, _ = alice.do("POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{"invitee_email": invitee, "role": "editor"})
	check("invite still returns a shareable link", status == 201 && jsonMap(body)["invite_url"] != "", status, string(body))
	time.Sleep(time.Second)
	check("no invite email is sent", len(mailpitMessages(invitee, "vault_invite")) == 0)

	newEmail := uniqueEmail("minimal-new")
	status, body, _ = alice.do("POST", "/account/v1/me/email", map[string]string{"password": "password123", "new_email": newEmail})
	check("email change applies directly without mail", status == 200 && jsonMap(body)["status"] == "changed", status, string(body))
	check("login with changed email", login(urlMinimal, newEmail, "password123") != nil)
}

func TestMultiTenantQuota(t *testing.T) {
	if urlMinimal == "" {
		t.Skip("CORTEX_E2E_URL_MINIMAL not set")
	}
	check := checker(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	owner := newUser(t, urlMinimal, "Quotaowner")
	vaultID := createVault(t, owner, "Quota")
	status, _, _ := owner.do("PATCH", "/vaults/v1/"+vaultID, map[string]any{"storage_quota_bytes": 100})
	check("only instance admins set quotas", status == 403, status)

	if _, err := pool.Exec(ctx, `UPDATE users SET is_instance_admin = true WHERE id = $1`, owner.userID); err != nil {
		t.Fatal(err)
	}
	status, body, _ := owner.do("PATCH", "/vaults/v1/"+vaultID, map[string]any{"storage_quota_bytes": 100})
	check("instance admin sets quota", status == 200 && jsonMap(body)["storage_quota_bytes"] == float64(100), status, string(body))

	status, _, _ = owner.upload(urlMinimal, vaultID, "a.md", bytes.Repeat([]byte("a"), 80))
	check("upload within quota", status == 201, status)
	status, body, _ = owner.upload(urlMinimal, vaultID, "b.md", bytes.Repeat([]byte("b"), 40))
	check("upload beyond quota rejected", status == 507, status, string(body))
	_, body, _ = owner.do("GET", "/vaults/v1/"+vaultID, nil)
	check("rejected upload does not count toward usage", jsonMap(body)["storage_used_bytes"] == float64(80), string(body))
	status, body, _ = owner.do("POST", "/sync/v1/vaults/"+vaultID+"/files/deltas", map[string]any{
		"file_path": "a.md", "base_version": 1, "checksum": "x", "size_bytes": 120,
		"encrypted_data": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("d"), 30)),
	})
	check("delta upload beyond quota rejected", status == 507, status, string(body))
}

func sealShare(t *testing.T, key []byte, plaintext []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write(plaintext)
	_ = zw.Close()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	return append(nonce, gcm.Seal(nil, nonce, compressed.Bytes(), nil)...)
}

func openShare(t *testing.T, key []byte, sealed []byte) []byte {
	t.Helper()
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, sealed[:12], sealed[12:], nil)
	if err != nil {
		t.Fatalf("decrypt shared note: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(plain))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(zr)
	return out
}

func TestNoteShares(t *testing.T) {
	check := checker(t)
	owner := newUser(t, urlA, "Shareowner")
	viewer := newUser(t, urlB, "Shareviewer")
	vaultID := createVault(t, owner, "Shares")
	base := "/sync/v1/vaults/" + vaultID
	owner.upload(urlA, vaultID, "notes/public.md", []byte("ciphertext-of-note"))
	owner.upload(urlA, vaultID, "notes/other.md", []byte("other"))
	_, body, _ := owner.do("POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{"invitee_email": viewer.email, "role": "viewer"})
	viewer.do("POST", "/vaults/v1/invites/accept", map[string]string{"token": jsonMap(body)["invite_token"].(string)})

	_, body, _ = owner.do("GET", "/vaults/v1/"+vaultID, nil)
	usageBefore := jsonMap(body)["storage_used_bytes"].(float64)

	wrapped := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 60))
	status, body, _ := viewer.do("POST", base+"/shares/", map[string]any{"file_path": "notes/public.md", "wrapped_key": wrapped})
	check("viewers cannot create public links", status == 403, status, string(body))
	status, _, _ = owner.do("POST", base+"/shares/", map[string]any{"file_path": "notes/missing.md", "wrapped_key": wrapped})
	check("sharing a missing note fails", status == 404, status)

	status, body, _ = owner.do("POST", base+"/shares/", map[string]any{"file_path": "notes/public.md", "wrapped_key": wrapped})
	share := jsonMap(body)
	shareID, _ := share["id"].(string)
	check("owner creates a public link", status == 201 && len(shareID) == 22 && strings.HasSuffix(share["url"].(string), "/s/"+shareID), status, string(body))
	status, body, _ = owner.do("POST", base+"/shares/", map[string]any{"file_path": "notes/public.md", "wrapped_key": wrapped})
	check("one link per note", status == 200 && jsonMap(body)["id"] == shareID, status, string(body))

	status, _, _ = call(urlB, "GET", "/share/v1/"+shareID, nil, nil)
	check("unpublished link is not readable", status == 404, status)

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	bundle := []byte(`{"v":1,"title":"Public","markdown":"# Hi","html":"<h1>Hi</h1>","attachments":[]}`)
	sealed := sealShare(t, key, bundle)
	status, body, _ = owner.doOn(urlA, "PUT", base+"/shares/"+shareID+"/content", sealed, map[string]string{"Content-Type": "application/octet-stream"})
	check("publish encrypted content", status == 200 && jsonMap(body)["revision"] == float64(1), status, string(body))

	status, content, headers := call(urlB, "GET", "/share/v1/"+shareID, nil, nil)
	check("anyone reads the ciphertext on any instance", status == 200 && bytes.Equal(openShare(t, key, content), bundle), status)
	status, _, _ = call(urlB, "GET", "/share/v1/"+shareID, nil, map[string]string{"If-None-Match": headers.Get("ETag")})
	check("unchanged shared note answers 304", status == 304, status)
	status, page, pageHeaders := call(urlA, "GET", "/s/"+shareID, nil, nil)
	check("viewer page has a strict CSP", status == 200 && strings.Contains(pageHeaders.Get("Content-Security-Policy"), "script-src 'self'") && bytes.Contains(page, []byte(`data-share-id="`+shareID+`"`)), status)
	status, _, assetHeaders := call(urlA, "GET", "/share-assets/viewer.js", nil, nil)
	check("viewer script served", status == 200 && strings.Contains(assetHeaders.Get("Content-Type"), "javascript"), status)

	_, body, _ = owner.do("GET", "/vaults/v1/"+vaultID, nil)
	check("shared content counts toward vault storage", jsonMap(body)["storage_used_bytes"].(float64) == usageBefore+float64(len(sealed)), string(body))

	_, body, _ = viewer.do("GET", base+"/shares/", nil)
	check("members list shared notes with the wrapped key", strings.Contains(string(body), shareID) && strings.Contains(string(body), wrapped), string(body))

	owner.do("POST", base+"/files/rename", map[string]string{"old_path": "notes/public.md", "new_path": "notes/renamed.md"})
	_, body, _ = owner.do("GET", base+"/shares/", nil)
	check("rename keeps the link on the new path", strings.Contains(string(body), `"file_path":"notes/renamed.md"`), string(body))

	owner.do("PATCH", "/vaults/v1/"+vaultID, map[string]any{"allow_public_links": false})
	status, _, _ = call(urlB, "GET", "/share/v1/"+shareID, nil, nil)
	check("vault policy disables existing links", status == 404, status)
	status, body, _ = owner.do("POST", base+"/shares/", map[string]any{"file_path": "notes/other.md", "wrapped_key": wrapped})
	check("vault policy blocks new links", status == 403 && jsonMap(body)["code"] == "public_links_disabled", status, string(body))
	owner.do("PATCH", "/vaults/v1/"+vaultID, map[string]any{"allow_public_links": true})

	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	status, _, _ = owner.do("PATCH", base+"/shares/"+shareID, map[string]any{"expires_at": past})
	check("expiry must be in the future", status == 400, status)
	soon := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	status, body, _ = owner.do("PATCH", base+"/shares/"+shareID, map[string]any{"expires_at": soon})
	check("set expiry", status == 200 && jsonMap(body)["expires_at"] != nil, status, string(body))

	owner.do("DELETE", base+"/files?path="+url.QueryEscape("notes/renamed.md"), nil)
	status, _, _ = call(urlB, "GET", "/share/v1/"+shareID, nil, nil)
	check("deleting the note revokes its link", status == 404, status)
	_, body, _ = owner.do("GET", "/vaults/v1/"+vaultID, nil)
	check("revoked content no longer counts toward storage", jsonMap(body)["storage_used_bytes"].(float64) == usageBefore, string(body))

	owner.upload(urlA, vaultID, "notes/again.md", []byte("x"))
	_, body, _ = owner.do("POST", base+"/shares/", map[string]any{"file_path": "notes/again.md", "wrapped_key": wrapped})
	againID := jsonMap(body)["id"].(string)
	owner.doOn(urlA, "PUT", base+"/shares/"+againID+"/content", sealed, nil)
	status, _, _ = owner.do("DELETE", base+"/shares/"+againID, nil)
	check("owner revokes a link", status == 204, status)
	status, page, _ = call(urlA, "GET", "/s/"+againID, nil, nil)
	check("revoked link shows a friendly page", status == 404 && bytes.Contains(page, []byte("<html")), status)

	if urlMinimal != "" {
		status, body, _ = call(urlMinimal, "GET", "/share/v1/"+againID, nil, nil)
		check("public links can be turned off per server", status == 404 && jsonMap(body)["code"] == "feature_disabled", status, string(body))
	}
}

func TestGracefulDrain(t *testing.T) {
	if drainCommand == "" {
		t.Skip("CORTEX_E2E_DRAIN_B not set")
	}
	check := checker(t)
	alice := newUser(t, urlA, "Drain")
	vaultID := createVault(t, alice, "Drain")

	drainSSE := openSSE(alice, urlB, vaultID)
	drainWS, _ := dialCollab(alice, urlA, urlB, vaultID, "drain.md", "")
	readUntil(drainWS, "synced", 3*time.Second)
	writeJSON(drainWS, map[string]any{"type": "update", "data": []byte("unflushed")})
	time.Sleep(100 * time.Millisecond)

	if out, err := exec.Command("sh", "-c", drainCommand).CombinedOutput(); err != nil {
		t.Fatalf("drain command: %v %s", err, out)
	}
	time.Sleep(500 * time.Millisecond)
	status, body, _ := call(urlB, "GET", "/ready", nil, nil)
	check("draining instance reports not ready", status == 503 && strings.Contains(string(body), "draining"), status, string(body))
	check("SSE closed on drain", drainSSE.closedWithin(5*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, _, readErr := drainWS.Read(ctx)
	cancel()
	check("collab socket closed with going-away on drain", websocket.CloseStatus(readErr) == websocket.StatusGoingAway, readErr)

	time.Sleep(500 * time.Millisecond)
	final, _ := dialCollab(alice, urlA, urlA, vaultID, "drain.md", "")
	fw := readUntil(final, "update", 3*time.Second)
	check("buffered collab update flushed during drain", fw["data"] == base64.StdEncoding.EncodeToString([]byte("unflushed")), fw)
	final.Close(websocket.StatusNormalClosure, "")
}
