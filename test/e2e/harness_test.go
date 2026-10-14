//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
)

var (
	urlA         = env("CORTEX_E2E_URL_A", "http://127.0.0.1:8080")
	urlB         = env("CORTEX_E2E_URL_B", "http://127.0.0.1:8081")
	urlMinimal   = os.Getenv("CORTEX_E2E_URL_MINIMAL")
	mailpitURL   = env("CORTEX_E2E_MAILPIT_URL", "http://127.0.0.1:8025")
	databaseURL  = env("CORTEX_E2E_DATABASE_URL", "postgres://cortex:cortex@127.0.0.1:5432/cortex_sync?sslmode=disable")
	drainCommand = os.Getenv("CORTEX_E2E_DRAIN_B")
	runID        = uuid.NewString()[:6]
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func uniqueEmail(name string) string {
	return name + runID + "@example.com"
}

func call(base, method, path string, body any, headers map[string]string) (int, []byte, http.Header) {
	var reader io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(b)
		contentType = "application/octet-stream"
	case url.Values:
		reader = strings.NewReader(b.Encode())
		contentType = "application/x-www-form-urlencoded"
	default:
		data, _ := json.Marshal(b)
		reader = bytes.NewReader(data)
		contentType = "application/json"
	}
	req, _ := http.NewRequest(method, base+path, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, []byte(err.Error()), http.Header{}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data, resp.Header
}

func jsonMap(data []byte) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

func jsonList(data []byte) []map[string]any {
	var l []map[string]any
	_ = json.Unmarshal(data, &l)
	return l
}

type client struct {
	base    string
	token   string
	refresh string
	device  string
	userID  string
	email   string
}

func (c *client) do(method, path string, body any) (int, []byte, http.Header) {
	return c.doOn(c.base, method, path, body, nil)
}

func (c *client) doOn(base, method, path string, body any, extra map[string]string) (int, []byte, http.Header) {
	headers := map[string]string{"Authorization": "Bearer " + c.token, "X-Device-ID": c.device}
	for k, v := range extra {
		headers[k] = v
	}
	return call(base, method, path, body, headers)
}

type result struct {
	status int
	body   []byte
}

func (c *client) attempt(method, path string, body any) result {
	status, data, _ := c.do(method, path, body)
	return result{status: status, body: data}
}

func (c *client) upload(base, vaultID, path string, content []byte) (int, []byte, http.Header) {
	return c.doOn(base, "POST", "/sync/v1/vaults/"+vaultID+"/files", content, map[string]string{
		"X-File-Path":          url.PathEscape(path),
		"X-File-Path-Encoding": "uri",
	})
}

func register(t *testing.T, base, email, name, locale string) {
	t.Helper()
	status, body, _ := call(base, "POST", "/auth/v1/register", map[string]string{
		"email": email, "password": "password123", "display_name": name, "locale": locale,
	}, nil)
	if status != 201 {
		t.Fatalf("register %s: %d %s", email, status, body)
	}
}

func login(base, email, password string) *client {
	c := &client{base: base, device: uuid.NewString(), email: email}
	status, body, _ := call(base, "POST", "/auth/v1/login", map[string]string{
		"email": email, "password": password, "device_id": c.device, "device_name": "e2e", "device_type": "desktop",
	}, nil)
	if status != 200 {
		return nil
	}
	m := jsonMap(body)
	c.token, _ = m["access_token"].(string)
	c.refresh, _ = m["refresh_token"].(string)
	c.userID, _ = m["user_id"].(string)
	return c
}

func newUser(t *testing.T, base, name string) *client {
	t.Helper()
	email := uniqueEmail(strings.ToLower(name))
	register(t, base, email, name, "en")
	c := login(base, email, "password123")
	if c == nil {
		t.Fatalf("login %s failed", email)
	}
	return c
}

func createVault(t *testing.T, c *client, name string) string {
	t.Helper()
	status, body, _ := c.do("POST", "/vaults/v1/", map[string]string{"name": name + " " + runID})
	if status != 201 {
		t.Fatalf("create vault: %d %s", status, body)
	}
	return jsonMap(body)["id"].(string)
}

type email struct {
	Subject string
	Text    string
}

func waitEmail(t *testing.T, to, template string, after int) (email, int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ids := mailpitMessages(to, template)
		if len(ids) > after {
			var msg struct{ Subject, Text string }
			_, body, _ := call(mailpitURL, "GET", "/api/v1/message/"+ids[0], nil, nil)
			_ = json.Unmarshal(body, &msg)
			return email{Subject: msg.Subject, Text: msg.Text}, len(ids)
		}
		time.Sleep(300 * time.Millisecond)
	}
	return email{}, 0
}

func mailpitMessages(to, template string) []string {
	_, body, _ := call(mailpitURL, "GET", "/api/v1/search?limit=100&query="+url.QueryEscape("to:"+to), nil, nil)
	var result struct {
		Messages []struct{ ID string } `json:"messages"`
	}
	_ = json.Unmarshal(body, &result)
	var ids []string
	for _, m := range result.Messages {
		var headers map[string][]string
		_, raw, _ := call(mailpitURL, "GET", "/api/v1/message/"+m.ID+"/headers", nil, nil)
		_ = json.Unmarshal(raw, &headers)
		if values := headers["X-Cortex-Template"]; len(values) > 0 && values[0] == template {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

var (
	tokenRe  = regexp.MustCompile(`token=([A-Za-z0-9_\-%]+)`)
	inviteRe = regexp.MustCompile(`/invite/([A-Za-z0-9_\-]+)`)
)

func tokenFrom(text string, re *regexp.Regexp) string {
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	t, _ := url.QueryUnescape(m[1])
	return t
}

type sseStream struct {
	events chan map[string]string
	closed chan struct{}
}

func openSSE(c *client, base, vaultID string) *sseStream {
	req, _ := http.NewRequest("GET", base+"/sync/v1/vaults/"+vaultID+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Device-ID", c.device)
	s := &sseStream{events: make(chan map[string]string, 256), closed: make(chan struct{})}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		close(s.closed)
		return s
	}
	go func() {
		defer close(s.closed)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		cur := map[string]string{}
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				cur["event"] = line[7:]
			case strings.HasPrefix(line, "data: "):
				cur["data"] = line[6:]
			case strings.HasPrefix(line, "id: "):
				cur["id"] = line[4:]
			case line == "" && len(cur) > 0:
				if cur["event"] != "ping" {
					s.events <- cur
				}
				cur = map[string]string{}
			}
		}
	}()
	time.Sleep(300 * time.Millisecond)
	return s
}

func (s *sseStream) waitFor(event string, timeout time.Duration) map[string]string {
	return s.waitMatch(timeout, func(e map[string]string) bool { return e["event"] == event }, nil)
}

func (s *sseStream) waitMatch(timeout time.Duration, match func(map[string]string) bool, skipped *[]map[string]string) map[string]string {
	deadline := time.After(timeout)
	for {
		select {
		case e := <-s.events:
			if match(e) {
				return e
			}
			if skipped != nil {
				*skipped = append(*skipped, e)
			}
		case <-deadline:
			return nil
		}
	}
}

func (s *sseStream) closedWithin(timeout time.Duration) bool {
	select {
	case <-s.closed:
		return true
	case <-time.After(timeout):
		return false
	}
}

func dialCollab(c *client, ticketBase, connectBase, vaultID, path, query string) (*websocket.Conn, error) {
	status, body, _ := c.doOn(ticketBase, "POST", "/sync/v1/vaults/"+vaultID+"/collab/tickets", map[string]string{"file_path": path}, nil)
	if status != 201 {
		return nil, fmt.Errorf("ticket: %d %s", status, body)
	}
	ticket := jsonMap(body)["ticket"].(string)
	wsURL := strings.Replace(connectBase, "http://", "ws://", 1) + "/sync/v1/vaults/" + vaultID +
		"/collab?path=" + url.QueryEscape(path) + "&ticket=" + url.QueryEscape(ticket) + query
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if conn != nil {
		conn.SetReadLimit(4 << 20)
	}
	return conn, err
}

func readTypesUntil(conn *websocket.Conn, last string, timeout time.Duration) []string {
	var types []string
	if conn == nil {
		return types
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return append(types, "error:"+err.Error())
		}
		if typ != websocket.MessageText {
			types = append(types, "binary")
			continue
		}
		var msg map[string]any
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		msgType, _ := msg["type"].(string)
		types = append(types, msgType)
		if msgType == last {
			return types
		}
	}
}

func readUntil(conn *websocket.Conn, msgType string, timeout time.Duration) map[string]any {
	if conn == nil {
		return map[string]any{"_error": "no connection"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return map[string]any{"_error": err.Error()}
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg map[string]any
		if json.Unmarshal(data, &msg) == nil && msg["type"] == msgType {
			return msg
		}
	}
}

func writeJSON(conn *websocket.Conn, msg any) {
	_ = wsjson.Write(context.Background(), conn, msg)
}

type batchFrame struct {
	Header map[string]any
	Body   []byte
}

func readBatch(body []byte) ([]batchFrame, error) {
	var frames []batchFrame
	r := bytes.NewReader(body)
	for r.Len() > 0 {
		var headerLen uint32
		if err := binary.Read(r, binary.BigEndian, &headerLen); err != nil {
			return nil, err
		}
		header := make([]byte, headerLen)
		if _, err := io.ReadFull(r, header); err != nil {
			return nil, err
		}
		var bodyLen uint64
		if err := binary.Read(r, binary.BigEndian, &bodyLen); err != nil {
			return nil, err
		}
		content := make([]byte, bodyLen)
		if _, err := io.ReadFull(r, content); err != nil {
			return nil, err
		}
		frame := batchFrame{Body: content}
		if err := json.Unmarshal(header, &frame.Header); err != nil {
			return nil, err
		}
		frames = append(frames, frame)
	}
	return frames, nil
}

func pngBytes() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for x := 0; x < 400; x++ {
		for y := 0; y < 200; y++ {
			img.Set(x, y, color.RGBA{B: uint8(y), A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func eventually(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return cond()
}
