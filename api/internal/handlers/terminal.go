package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"kubernetes.getvesta.sh/api/internal/models"
)

// ExecWS opens a web terminal to a pod via WebSocket.
func (h *Handler) ExecWS(c *gin.Context) {
	targetNS, _, ok := h.appPodTarget(c)
	if !ok {
		return
	}
	h.serveExec(c, targetNS, appPodSelector(c.Param("appId")))
}

// serveExec opens a terminal to one of the pods matching selector -- the one named by
// ?pod=, which must match, or the first.
func (h *Handler) serveExec(c *gin.Context, targetNS, labelSelector string) {
	podName := c.Query("pod")
	container := c.Query("container")

	if podName != "" {
		if !h.podInSelection(c, targetNS, podName, labelSelector) {
			return
		}
	} else {
		pods, err := h.K8s.ListPods(c.Request.Context(), targetNS, labelSelector)
		if err != nil || len(pods) == 0 {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "no pods found"})
			return
		}
		podName = pods[0].Name
		if container == "" && len(pods[0].Spec.Containers) > 0 {
			container = pods[0].Spec.Containers[0].Name
		}
	}

	// Upgrade to WebSocket
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Build exec request
	cmd := []string{"/bin/sh", "-c", "TERM=xterm exec sh"}
	req := h.K8s.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(targetNS).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   cmd,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(h.K8s.Config, "POST", req.URL())
	if err != nil {
		msg, _ := json.Marshal(map[string]string{"type": "error", "message": fmt.Sprintf("exec error: %v", err)})
		conn.WriteMessage(websocket.TextMessage, msg)
		return
	}

	// Bridge WebSocket <-> SPDY exec
	wsStream := &wsTerminalStream{conn: conn}

	err = exec.StreamWithContext(c.Request.Context(), remotecommand.StreamOptions{
		Stdin:  wsStream,
		Stdout: wsStream,
		Stderr: wsStream,
		Tty:    true,
	})
	if err != nil {
		msg, _ := json.Marshal(map[string]string{"type": "error", "message": fmt.Sprintf("stream ended: %v", err)})
		conn.WriteMessage(websocket.TextMessage, msg)
	}
}

// wsTerminalStream bridges a WebSocket connection to a remotecommand stream.
type wsTerminalStream struct {
	conn    *websocket.Conn
	readBuf []byte
	mu      sync.Mutex
}

// Read reads from the WebSocket (stdin from the client).
func (ws *wsTerminalStream) Read(p []byte) (int, error) {
	if len(ws.readBuf) > 0 {
		n := copy(p, ws.readBuf)
		ws.readBuf = ws.readBuf[n:]
		return n, nil
	}

	_, message, err := ws.conn.ReadMessage()
	if err != nil {
		return 0, io.EOF
	}

	// Try to parse as JSON message with type field
	var msg struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}
	if json.Unmarshal(message, &msg) == nil && msg.Type == "input" {
		message = []byte(msg.Data)
	}

	n := copy(p, message)
	if n < len(message) {
		ws.readBuf = message[n:]
	}
	return n, nil
}

// Write writes to the WebSocket (stdout/stderr to the client).
func (ws *wsTerminalStream) Write(p []byte) (int, error) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	msg, _ := json.Marshal(map[string]string{
		"type": "output",
		"data": string(p),
	})
	err := ws.conn.WriteMessage(websocket.TextMessage, msg)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}
