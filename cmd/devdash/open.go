package main

import (
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	devdash "github.com/clay-coffman/devdash"
)

const chromeApp = "/Applications/Google Chrome.app"

// Only loopback URLs with an explicit port: the dedicated browser exists to
// reach the host's localhost, nothing else.
var localURL = regexp.MustCompile(`^https?://(localhost|127\.0\.0\.1):([0-9]{1,5})([/#?]|$)`)

// tunnelPort derives a stable local SOCKS port from the host alias, so two
// hosts never share one and nothing needs configuring. Override with -port.
func tunnelPort(host string) int {
	h := fnv.New32a()
	h.Write([]byte(host))
	return 10900 + int(h.Sum32()%100)
}

func listening(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func sshHostOK(host string) error {
	if host == "" || strings.HasPrefix(host, "-") || strings.ContainsAny(host, " \t/") {
		return fmt.Errorf("bad ssh host %q", host)
	}
	return nil
}

// ensureTunnel starts `ssh -N -D` detached if nothing listens on port.
func ensureTunnel(host string, port int) error {
	if listening(port) {
		return nil
	}
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, "Library", "Logs", "devdash")
	if runtime.GOOS != "darwin" {
		logDir = filepath.Join(home, ".local", "state", "devdash")
	}
	os.MkdirAll(logDir, 0o755)
	logf, err := os.OpenFile(filepath.Join(logDir, "tunnel-"+host+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	fmt.Fprintf(logf, "%s starting ssh -N -D 127.0.0.1:%d %s\n", time.Now().Format(time.RFC3339), port, host)
	cmd := exec.Command("ssh", "-N", "-D", fmt.Sprintf("127.0.0.1:%d", port),
		"-o", "ExitOnForwardFailure=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3",
		"-o", "ConnectTimeout=15", "-o", "ForwardAgent=no", host)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survives this process exiting
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ssh: %w", err)
	}
	fmt.Fprintf(os.Stderr, "devdash: starting tunnel to %s on 127.0.0.1:%d (pid %d)\n", host, port, cmd.Process.Pid)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			b, _ := os.ReadFile(logf.Name())
			return fmt.Errorf("ssh exited (%v); log tail:\n%s", err, tailStr(string(b), 600))
		default:
		}
		if listening(port) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("tunnel did not come up within 40 s (ssh may be waiting for a passphrase or key approval; check " + logf.Name() + ")")
}

func tailStr(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

func openCmd(args []string) error {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	port := fs.Int("port", 0, "local SOCKS port (default: derived from the host name)")
	check := fs.Bool("check", false, "only test the tunnel")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: devdash open [-port N] [--check] <ssh-host> [http://localhost:PORT/path ...]")
	}
	// Allow flags after the host too.
	var rest []string
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			fs.Parse(args[i:])
			rest = append(rest, fs.Args()...)
			break
		}
		rest = append(rest, a)
	}
	if len(rest) == 0 {
		fs.Usage()
		return errors.New("missing ssh host")
	}
	host, urls := rest[0], rest[1:]
	if err := sshHostOK(host); err != nil {
		return err
	}
	if *port == 0 {
		*port = tunnelPort(host)
	}
	for _, u := range urls {
		if !localURL.MatchString(u) || strings.ContainsAny(u, " \t\n") {
			return fmt.Errorf("expected http(s)://localhost:PORT/path, got %q", u)
		}
	}
	if runtime.GOOS != "darwin" {
		return errors.New("`open` runs on the Mac; on the host use `devdash serve` or `devdash install-service`")
	}
	if err := ensureTunnel(host, *port); err != nil {
		return err
	}
	if *check {
		fmt.Printf("tunnel to %s listening on 127.0.0.1:%d\n", host, *port)
		return nil
	}
	if _, err := os.Stat(chromeApp); err != nil {
		return errors.New("Google Chrome is not installed in /Applications")
	}
	if len(urls) == 0 {
		urls = []string{fmt.Sprintf("http://localhost:%s/", strings.TrimPrefix(defaultListen, "127.0.0.1:"))}
	}
	home, _ := os.UserHomeDir()
	profile := filepath.Join(home, "Library", "Application Support", "devdash", host)
	// A separate user-data directory per host keeps cookies apart and stops a
	// running Chrome from ignoring the proxy flags. <-loopback> removes
	// Chrome's implicit localhost bypass so localhost resolves on the host.
	// No DIRECT fallback: a dead tunnel fails the request instead of hitting
	// this Mac's own ports.
	oargs := append([]string{"-na", chromeApp, "--args",
		"--user-data-dir=" + profile,
		fmt.Sprintf("--proxy-server=socks5://127.0.0.1:%d", *port),
		"--proxy-bypass-list=<-loopback>",
		"--no-first-run", "--no-default-browser-check"}, urls...)
	return run("open", oargs...)
}

func setupCmd(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	binary := fs.String("binary", "", "copy this local linux binary instead of downloading a release (for development)")
	port := fs.Int("port", 0, "local SOCKS port (default: derived from the host name)")
	noOpen := fs.Bool("no-open", false, "do not open the dashboard afterwards")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, "usage: devdash setup [-binary path] [-no-open] <ssh-host>") }
	var rest []string
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			fs.Parse(args[i:])
			rest = append(rest, fs.Args()...)
			break
		}
		rest = append(rest, a)
	}
	if len(rest) != 1 {
		fs.Usage()
		return errors.New("expected exactly one ssh host")
	}
	host := rest[0]
	if err := sshHostOK(host); err != nil {
		return err
	}
	ssh := func(stdin string, remote string) error {
		cmd := exec.Command("ssh", "-o", "ConnectTimeout=15", host, remote)
		cmd.Stdin = strings.NewReader(stdin)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	fmt.Fprintf(os.Stderr, "devdash: installing on %s\n", host)
	if *binary != "" {
		cmd := exec.Command("scp", "-q", *binary, host+":.local/bin/devdash.new")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("scp: %w", err)
		}
		if err := ssh("", "mkdir -p ~/.local/bin && mv ~/.local/bin/devdash.new ~/.local/bin/devdash && chmod 755 ~/.local/bin/devdash"); err != nil {
			return err
		}
	} else {
		env := ""
		if version != "dev" {
			env = "DEVDASH_VERSION=" + version + " "
		}
		if err := ssh(devdash.InstallScript, env+"sh -s"); err != nil {
			return fmt.Errorf("installer on %s failed: %w", host, err)
		}
	}
	if err := ssh("", "~/.local/bin/devdash install-service"); err != nil {
		return fmt.Errorf("install-service on %s failed: %w", host, err)
	}
	if err := ssh("", "sleep 2; curl -fsS -o /dev/null http://"+defaultListen+"/api/state && echo 'devdash: dashboard is answering on "+defaultListen+"'"); err != nil {
		return fmt.Errorf("the service started but did not answer on %s: %w", defaultListen, err)
	}
	if *noOpen || runtime.GOOS != "darwin" {
		return nil
	}
	oargs := []string{host}
	if *port != 0 {
		oargs = append(oargs, "-port", fmt.Sprint(*port))
	}
	return openCmd(oargs)
}
