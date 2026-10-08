package work

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/clay-coffman/devdash/internal/collect"
)

var serverID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// Route is cached evidence of the endpoint observed by the Herdr CLI.
// Its socket is internal and never included in the browser projection.
type Route struct {
	Server   string `json:"server,omitempty"`
	Verified bool   `json:"verified"`
	Reason   string `json:"reason,omitempty"`
}

func SocketContext() string {
	if socket := os.Getenv("HERDR_SOCKET_PATH"); socket != "" {
		return socket
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "herdr", "herdr.sock")
}

func Fingerprint(socket string) string {
	sum := sha256.Sum256([]byte(socket))
	return fmt.Sprintf("%x", sum[:8])
}

// VerifyRoute is a read-only, bounded background probe. Snapshot APIs have no
// endpoint identity; status server --json supplies the observed socket instead.
func VerifyRoute(socket string) Route {
	if socket == "" {
		return Route{Reason: "Herdr socket context unavailable"}
	}
	r := Route{Server: Fingerprint(socket)}
	raw, err := collect.RunBoundedSocket(5*time.Second, 4096, socket, "herdr", "status", "server", "--json")
	if err != nil {
		r.Reason = "Herdr endpoint verification unavailable"
		return r
	}
	var status struct {
		Socket     string `json:"socket"`
		Running    bool   `json:"running"`
		Compatible bool   `json:"endpoint_compatible"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if dec.Decode(&status) != nil || dec.Decode(new(any)) != io.EOF || !status.Running || !status.Compatible || status.Socket != socket {
		r.Reason = "Herdr endpoint does not match configured socket"
		return r
	}
	r.Verified = true
	return r
}
