package cli

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"memory/internal/engine"
	"memory/internal/server"
	"memory/web"
)

// notAvailable is shared with cmd_skill.go until that prompt lands.
const notAvailable = "not available in this build yet"

type hostList []string

func (h *hostList) String() string { return strings.Join(*h, ",") }
func (h *hostList) Set(v string) error {
	*h = append(*h, v)
	return nil
}

type serverFlags struct {
	port         int
	host         string
	token        string
	tokenFile    string
	open         bool
	allowedHosts hostList
}

func serverCommand() command {
	return command{
		name:  "server",
		usage: "mem server [--port N] [--host H] [--allowed-host HOST] [--token T|--token-file F] [--open]",
		parse: func(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
			var sf serverFlags
			fs.IntVar(&sf.port, "port", 0, "dashboard port (default from config)")
			fs.StringVar(&sf.host, "host", "127.0.0.1", "host to bind (default 127.0.0.1)")
			fs.Var(&sf.allowedHosts, "allowed-host", "allowed host header for reverse proxies or tunnels (repeatable)")
			fs.StringVar(&sf.token, "token", "", "bearer token for API authentication")
			fs.StringVar(&sf.tokenFile, "token-file", "", "path to file containing bearer token")
			fs.BoolVar(&sf.open, "open", false, "open dashboard in the default browser")
			pos, err := collectArgs(fs, args)
			if err != nil {
				return nil, err
			}
			a.state = sf
			return pos, nil
		},
		run: runServer,
	}
}

func runServer(a *app, pos []string) (int, error) {
	if len(pos) > 0 {
		return ExitUsage, usagef("mem server takes no arguments")
	}

	sf, _ := a.state.(serverFlags)

	if err := a.loadConfig(); err != nil {
		return ExitError, err
	}

	port := sf.port
	if port <= 0 {
		port = a.cfg.Port
	}
	if port <= 0 {
		port = 7777
	}

	host := strings.TrimSpace(sf.host)
	if host == "" {
		host = "127.0.0.1"
	}

	token := sf.token
	if token == "" && sf.tokenFile != "" {
		b, err := os.ReadFile(sf.tokenFile)
		if err != nil {
			return ExitError, fmt.Errorf("read token file: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}

	// Merge allowed hosts from bind host, config, and flags
	allowedHosts := []string{host}
	for _, h := range a.cfg.AllowedHosts {
		if t := strings.TrimSpace(h); t != "" {
			allowedHosts = append(allowedHosts, t)
		}
	}
	for _, h := range sf.allowedHosts {
		if t := strings.TrimSpace(h); t != "" {
			allowedHosts = append(allowedHosts, t)
		}
	}

	if !isLoopbackHost(host) && token == "" {
		return ExitUsage, usagef("refusing to bind non-loopback host %q without an authentication token (pass --token or --token-file)", host)
	}

	for _, ah := range append(sf.allowedHosts, a.cfg.AllowedHosts...) {
		if !isLoopbackHost(ah) && token == "" {
			return ExitUsage, usagef("refusing to start with non-loopback allowed host %q without an authentication token (pass --token or --token-file)", ah)
		}
	}

	// The dashboard is long-lived, so keep quantized vectors in RAM.
	eng, err := a.openEngineWith(engine.WithResidentVectors(true))
	if err != nil {
		return ExitError, fmt.Errorf("open engine: %w", err)
	}
	defer eng.Close()

	// Initial sweep of expired contexts
	_, _ = eng.Store.SweepExpired()

	ctx, stop := signal.NotifyContext(a.context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Sweep expired contexts periodically
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_, _ = eng.Store.SweepExpired()
			case <-ctx.Done():
				return
			}
		}
	}()

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if isPortInUse(err) {
			return ExitError, fmt.Errorf("port %d is already in use; specify a different port with --port <port> or stop the existing process", port)
		}
		return ExitError, fmt.Errorf("bind %s: %w", addr, err)
	}
	defer ln.Close()

	actualPort := ln.Addr().(*net.TCPAddr).Port
	baseURL := fmt.Sprintf("http://%s", net.JoinHostPort(host, strconv.Itoa(actualPort)))
	displayURL := baseURL
	if token != "" {
		displayURL = fmt.Sprintf("%s/?token=%s", baseURL, token)
	}

	a.say("mem dashboard listening on %s\n", displayURL)

	if sf.open {
		go func() {
			if err := openBrowser(displayURL); err != nil {
				fmt.Fprintf(a.io.Err, "warning: could not launch browser: %v\n", err)
			}
		}()
	}

	handler := server.New(eng, server.Options{
		Version:      Version,
		Token:        token,
		AllowedHosts: allowedHosts,
		DataDir:      a.cfg.DataDir,
		Config:       a.cfg,
		StartedAt:    time.Now(),
		Assets:       web.Dist(),
		Logger:       log.New(a.io.Err, "[server] ", log.LstdFlags),
	})

	if err := server.ServeListener(ctx, ln, handler); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return ExitError, fmt.Errorf("server error: %w", err)
	}
	return ExitOK, nil
}

func isLoopbackHost(h string) bool {
	h = strings.TrimSpace(h)
	if hostOnly, _, err := net.SplitHostPort(h); err == nil {
		h = hostOnly
	}
	h = strings.Trim(h, "[]")
	h = strings.ToLower(h)
	if h == "127.0.0.1" || h == "localhost" || h == "::1" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func isPortInUse(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "bind: only one usage of each socket address") ||
		strings.Contains(msg, "wsaeaddrinuse") ||
		strings.Contains(msg, "already in use")
}

func openBrowser(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", targetURL)
	case "darwin":
		cmd = exec.Command("open", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	return cmd.Start()
}
