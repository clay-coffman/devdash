package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func selfPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// servicePATH is what the service sees. Tools devdash shells out to (docker,
// herdr, compose-stack-reaper, git) must be on it; a login shell's PATH is
// not inherited by systemd --user or launchd.
func servicePATH(home string) string {
	want := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".local", "share", "mise", "shims"),
		filepath.Join(home, ".asdf", "shims"),
		filepath.Join(home, ".cargo", "bin"),
		"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin",
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range append(want, filepath.SplitList(os.Getenv("PATH"))...) {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

func installService(args []string) error {
	if len(args) > 0 {
		return errors.New("install-service takes no arguments")
	}
	switch runtime.GOOS {
	case "linux":
		return installSystemd()
	case "darwin":
		return installLaunchd()
	}
	return fmt.Errorf("no service support for %s", runtime.GOOS)
}

func uninstallService() error {
	switch runtime.GOOS {
	case "linux":
		run("systemctl", "--user", "disable", "--now", "devdash")
		home, _ := os.UserHomeDir()
		os.Remove(filepath.Join(home, ".config", "systemd", "user", "devdash.service"))
		run("systemctl", "--user", "daemon-reload")
		fmt.Println("devdash service removed")
		return nil
	case "darwin":
		home, _ := os.UserHomeDir()
		plist := filepath.Join(home, "Library", "LaunchAgents", "dev.devdash.plist")
		run("launchctl", "bootout", fmt.Sprintf("gui/%d/dev.devdash", os.Getuid()))
		os.Remove(plist)
		fmt.Println("devdash launch agent removed")
		return nil
	}
	return fmt.Errorf("no service support for %s", runtime.GOOS)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func installSystemd() error {
	self, err := selfPath()
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return err
	}
	unit := fmt.Sprintf(`[Unit]
Description=devdash host dashboard
After=default.target

[Service]
ExecStart=%s serve
Restart=on-failure
RestartSec=5
Environment=PATH=%s
Environment=HOME=%s
# Never let the dashboard be what the OOM killer takes first.
OOMScoreAdjust=100

[Install]
WantedBy=default.target
`, self, servicePATH(home), home)
	// A transient unit of the same name (systemd-run) would collide.
	exec.Command("systemctl", "--user", "stop", "devdash").Run()
	exec.Command("systemctl", "--user", "reset-failed", "devdash").Run()
	if err := os.WriteFile(filepath.Join(unitDir, "devdash.service"), []byte(unit), 0o644); err != nil {
		return err
	}
	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w (is a systemd user session running?)", err)
	}
	if err := run("systemctl", "--user", "enable", "--now", "devdash"); err != nil {
		return fmt.Errorf("enable: %w", err)
	}
	// Linger keeps user services alive without a login session, which is
	// what a devbox reached only over ssh needs.
	out, _ := exec.Command("loginctl", "show-user", os.Getenv("USER"), "-p", "Linger").Output()
	if strings.TrimSpace(string(out)) == "Linger=no" {
		if err := exec.Command("loginctl", "enable-linger").Run(); err != nil {
			fmt.Println("note: run `sudo loginctl enable-linger " + os.Getenv("USER") + "` so the service survives logout")
		} else {
			fmt.Println("enabled linger so the service survives logout")
		}
	}
	fmt.Printf("devdash service enabled; ExecStart=%s serve on http://%s/\n", self, defaultListen)
	return nil
}

func installLaunchd() error {
	self, err := selfPath()
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, "Library", "Logs", "devdash")
	os.MkdirAll(logDir, 0o755)
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "dev.devdash.plist")
	os.MkdirAll(filepath.Dir(plistPath), 0o755)
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>dev.devdash</string>
  <key>ProgramArguments</key><array><string>%s</string><string>serve</string></array>
  <key>EnvironmentVariables</key><dict><key>PATH</key><string>%s</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>%s/serve.log</string>
  <key>StandardErrorPath</key><string>%s/serve.log</string>
</dict>
</plist>
`, self, servicePATH(home), logDir, logDir)
	target := fmt.Sprintf("gui/%d", os.Getuid())
	exec.Command("launchctl", "bootout", target+"/dev.devdash").Run()
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}
	if err := run("launchctl", "bootstrap", target, plistPath); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w", err)
	}
	fmt.Printf("devdash launch agent loaded; serving on http://%s/\n", defaultListen)
	return nil
}
