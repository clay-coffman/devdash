package collect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

var ErrOutputLimit = errors.New("command output exceeds limit")

// cappedBuffer bounds memory and cancels a producer as soon as the limit is hit.
type cappedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	cancel   context.CancelFunc
	exceeded bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buffer.Len() {
		b.exceeded = true
		b.cancel()
		return 0, ErrOutputLimit
	}
	return b.buffer.Write(p)
}

// RunBounded uses the service's environment, including HERDR_SOCKET_PATH.
// Diagnostics are bounded and intentionally not returned: tool output can contain
// unrelated private data. Only command/timeout/size failures reach the browser.
func RunBounded(timeout time.Duration, limit int, name string, args ...string) ([]byte, error) {
	return RunBoundedSocket(timeout, limit, "", name, args...)
}

// RunBoundedSocket pins the child command to one endpoint, without mutating
// the process or machine environment. An empty socket preserves old callers.
func RunBoundedSocket(timeout time.Duration, limit int, socket, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if socket != "" {
		env := make([]string, 0, len(os.Environ())+1)
		for _, v := range os.Environ() {
			if len(v) < len("HERDR_SOCKET_PATH=") || v[:len("HERDR_SOCKET_PATH=")] != "HERDR_SOCKET_PATH=" {
				env = append(env, v)
			}
		}
		cmd.Env = append(env, "HERDR_SOCKET_PATH="+socket)
	}
	cmd.WaitDelay = 200 * time.Millisecond // don't wait forever for inherited pipes
	stdout := &cappedBuffer{limit: limit, cancel: cancel}
	stderr := &cappedBuffer{limit: 4096, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, fmt.Errorf("%s: %w", name, ErrOutputLimit)
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s: %w", name, ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return stdout.buffer.Bytes(), nil
}
