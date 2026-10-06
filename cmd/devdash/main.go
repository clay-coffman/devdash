// devdash: a live dashboard for a development host, and the launcher that
// opens it from a Mac through an SSH SOCKS tunnel.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/clay-coffman/devdash/internal/server"
	"github.com/clay-coffman/devdash/internal/state"
)

// version is set by the release build: -ldflags "-X main.version=v0.1.0".
var version = "dev"

const defaultListen = "127.0.0.1:9900"

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  devdash serve [-listen 127.0.0.1:9900]       run the dashboard on this host
  devdash install-service                      run it as a user service (systemd or launchd), enabled at login
  devdash uninstall-service                    stop and remove that service
  devdash open <ssh-host> [url ...]            (Mac) tunnel to the host and open its dashboard in Chrome
  devdash open <ssh-host> --check              (Mac) only test the tunnel
  devdash setup <ssh-host>                     (Mac) install on the host over ssh, enable the service, open it
  devdash version
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "install-service":
		err = installService(os.Args[2:])
	case "uninstall-service":
		err = uninstallService()
	case "open":
		err = openCmd(os.Args[2:])
	case "setup":
		err = setupCmd(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "devdash:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", defaultListen, "address to listen on (loopback only)")
	fs.Parse(args)
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || (host != "127.0.0.1" && host != "::1" && host != "localhost") {
		return fmt.Errorf("refusing to listen on %q: devdash binds loopback only", *listen)
	}

	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return err
	}
	c := state.New(hex.EncodeToString(tok))
	stop := make(chan struct{})
	go c.Run(stop)

	srv := &http.Server{Addr: *listen, Handler: server.New(c)}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		close(stop)
		srv.Close()
	}()
	log.Printf("devdash %s listening on http://%s/", version, *listen)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
