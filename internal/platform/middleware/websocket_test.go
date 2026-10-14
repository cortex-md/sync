package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestWebSocketUpgradeThroughLoggingAndMetrics(t *testing.T) {
	upgrade := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte("hi"))
		conn.Close(websocket.StatusNormalClosure, "")
	})
	srv := httptest.NewServer(RequestID(Logger(Metrics(upgrade))))
	defer srv.Close()

	conn, _, err := websocket.Dial(context.Background(), strings.Replace(srv.URL, "http://", "ws://", 1), nil)
	require.NoError(t, err)
	_, data, err := conn.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, "hi", string(data))
}
