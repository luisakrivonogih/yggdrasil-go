package dashboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type fakeLogger struct{}

func (fakeLogger) Printf(format string, args ...interface{}) {}
func (fakeLogger) Warnln(args ...interface{})                {}
func (fakeLogger) Errorln(args ...interface{})               {}

func TestResolveEntryPointUsesConfiguredPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("// fake"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	entry, err := resolveEntryPoint(dir)
	if err != nil {
		t.Fatalf("resolveEntryPoint returned error: %v", err)
	}
	want := filepath.Join(dir, "index.js")
	if entry != want {
		t.Fatalf("entry = %q, want %q", entry, want)
	}
}

func TestResolveEntryPointErrorsWhenNotFound(t *testing.T) {
	dir := t.TempDir() // empty, no index.js
	if _, err := resolveEntryPoint(dir); err == nil {
		t.Fatal("resolveEntryPoint returned nil error, want an error for a missing build")
	}
}

func TestSplitHostPort(t *testing.T) {
	host, port, err := splitHostPort("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("splitHostPort returned error: %v", err)
	}
	if host != "127.0.0.1" || port != "8080" {
		t.Fatalf("host, port = %q, %q, want \"127.0.0.1\", \"8080\"", host, port)
	}
}

func TestSplitHostPortStripsIPv6Brackets(t *testing.T) {
	// adapter-node's server.listen() cannot bind the bracketed literal -
	// HOST must be the bare address.
	host, port, err := splitHostPort("[::1]:8080")
	if err != nil {
		t.Fatalf("splitHostPort returned error: %v", err)
	}
	if host != "::1" || port != "8080" {
		t.Fatalf("host, port = %q, %q, want \"::1\", \"8080\"", host, port)
	}
}

func TestSplitHostPortRejectsMissingColon(t *testing.T) {
	if _, _, err := splitHostPort("notahostport"); err == nil {
		t.Fatal("splitHostPort returned nil error, want an error")
	}
}

func TestStartRejectsDisabledAdminSocket(t *testing.T) {
	if _, err := Start(Config{Listen: "127.0.0.1:8080", AdminListen: "none"}, fakeLogger{}); err == nil {
		t.Fatal("Start returned nil error, want an error when AdminListen is \"none\"")
	}
}

func TestStartErrorsWhenNoDashboardBuildFound(t *testing.T) {
	empty := t.TempDir()
	cfg := Config{Listen: "127.0.0.1:8080", AdminListen: "unix:///tmp/test.sock", Path: empty}
	if _, err := Start(cfg, fakeLogger{}); err == nil {
		t.Fatal("Start returned nil error, want an error for a missing dashboard build")
	}
}

func TestStartAndStopSpawnsRealNodeProcess(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed, skipping real-spawn test")
	}
	dir := t.TempDir()
	script := "process.stdout.write('dashboard test process running\\n'); setInterval(() => {}, 1000);"
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(script), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	cfg := Config{Listen: "127.0.0.1:0", AdminListen: "unix:///tmp/test.sock", Path: dir}
	p, err := Start(cfg, fakeLogger{})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestStopIsSafeOnNilProcess(t *testing.T) {
	var p *Process
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop on nil *Process returned error: %v", err)
	}
}
