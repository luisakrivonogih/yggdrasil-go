// Package dashboard spawns and supervises the local operator
// dashboard's Node.js child process (a separately-built SvelteKit
// adapter-node app, see yggdashboard/) when configured. It never talks
// to the admin socket itself - it only starts the process that does,
// passing it the node's own AdminListen address as an environment
// variable so the dashboard needs no separate admin-socket
// configuration. A missing `node` binary or missing build output is
// always returned as an error for the caller to log as a warning - this
// package must never be the reason yggdrasil itself fails to start.
package dashboard

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Config holds what Start needs to spawn the dashboard.
type Config struct {
	Listen      string // dashboard's own host:port
	Path        string // directory containing build/index.js; "" tries defaultPaths
	AdminListen string // the node's own admin socket address, reused as-is
}

// Logger is the minimal logging interface Start/Process need - already
// satisfied by *log.Logger (gologme/log), the logger used throughout
// cmd/yggdrasil/main.go.
type Logger interface {
	Printf(format string, args ...interface{})
	Warnln(args ...interface{})
	Errorln(args ...interface{})
}

// defaultPaths are conventional locations for the dashboard's built
// assets, tried in order when Config.Path is empty.
var defaultPaths = []string{
	"/usr/lib/yggdrasil/dashboard",
	"/usr/share/yggdrasil/dashboard",
	"./yggdashboard/build",
}

// resolveEntryPoint returns the path to a build/index.js under the
// configured directory, or - if configured is empty - the first
// defaultPaths entry that has one.
func resolveEntryPoint(configured string) (string, error) {
	candidates := defaultPaths
	if configured != "" {
		candidates = []string{configured}
	}
	for _, dir := range candidates {
		entry := filepath.Join(dir, "index.js")
		if info, err := os.Stat(entry); err == nil && !info.IsDir() {
			return entry, nil
		}
	}
	return "", fmt.Errorf("dashboard: no built dashboard found (tried %v) - run 'npm run build' in yggdashboard/ and set dashboard.path, or install it to a conventional location", candidates)
}

// splitHostPort splits a "host:port" listen address into its parts for
// the environment variables the dashboard process expects.
func splitHostPort(listen string) (host, port string, err error) {
	idx := bytes.LastIndexByte([]byte(listen), ':')
	if idx < 0 {
		return "", "", fmt.Errorf("dashboard: invalid listen address %q, want host:port", listen)
	}
	return listen[:idx], listen[idx+1:], nil
}

// Process supervises the dashboard's Node.js child process.
type Process struct {
	cmd *exec.Cmd
}

// Start validates cfg, resolves the dashboard's built entry point and
// the `node` binary, and spawns it. Every failure mode here is returned
// as an error rather than panicking - the caller (cmd/yggdrasil) must
// treat a failed Start as a warning, not a reason to stop the daemon.
func Start(cfg Config, logger Logger) (*Process, error) {
	if cfg.AdminListen == "" || cfg.AdminListen == "none" {
		return nil, fmt.Errorf("dashboard: AdminListen is disabled (\"none\") - the dashboard has nothing to poll")
	}
	host, port, err := splitHostPort(cfg.Listen)
	if err != nil {
		return nil, err
	}
	entry, err := resolveEntryPoint(cfg.Path)
	if err != nil {
		return nil, err
	}
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		return nil, fmt.Errorf("dashboard: 'node' not found on PATH: %w", err)
	}

	cmd := exec.Command(nodeBin, entry)
	cmd.Env = append(os.Environ(),
		"ADMIN_SOCKET="+cfg.AdminListen,
		// HOST/PORT (not a custom name) - @sveltejs/adapter-node's
		// built server reads these itself when run directly as
		// `node build/index.js`; no custom server wrapper needed.
		"HOST="+host,
		"PORT="+port,
	)
	cmd.Stdout = &prefixWriter{logger: logger}
	cmd.Stderr = &prefixWriter{logger: logger}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("dashboard: failed to start: %w", err)
	}
	logger.Printf("Dashboard started (pid %d), listening on http://%s", cmd.Process.Pid, cfg.Listen)
	return &Process{cmd: cmd}, nil
}

// Stop terminates the dashboard child process. Safe to call on a nil
// *Process.
func (p *Process) Stop() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// prefixWriter forwards each line written to it to logger.Printf,
// prefixed so the dashboard child process's output is visibly distinct
// from yggdrasil's own log lines in combined output (journald, log
// files).
type prefixWriter struct {
	logger Logger
	buf    []byte
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.logger.Printf("dashboard: %s", string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}
