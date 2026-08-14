package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

//go:embed web/*
var webFS embed.FS

func main() {
	setupLog()

	var (
		addr   = flag.String("addr", "127.0.0.1:8787", "listen address (localhost only — this process owns USB)")
		apiURL = flag.String("api", envOr("API_URL", "http://localhost:8080"), "MechMind API base URL")
		open   = flag.Bool("open", true, "open the GUI in a browser")
	)
	flag.Parse()

	bay := &bayServer{
		apiURL: *apiURL,
		token:  loadSavedToken(),
		http:   &http.Client{Timeout: 45 * time.Second},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", bay.serveIndex)
	mux.HandleFunc("GET /local/state", bay.handleState)
	mux.HandleFunc("GET /local/devices", bay.handleDevices)
	mux.HandleFunc("POST /local/login", bay.handleLogin)
	mux.HandleFunc("POST /local/register", bay.handleRegister)
	mux.HandleFunc("POST /local/logout", bay.handleLogout)
	mux.HandleFunc("POST /local/scan", bay.handleScan)
	mux.HandleFunc("GET /local/scan/status", bay.handleScanStatus)
	mux.HandleFunc("POST /local/scan/cancel", bay.handleScanCancel)
	mux.HandleFunc("POST /local/quit", bay.handleQuit)
	mux.HandleFunc("GET /local/explain", bay.handleExplain)

	url := "http://" + *addr + "/"
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		if addrInUse(err) {
			log.Printf("already running on %s — opening the browser", url)
			if *open {
				if oerr := openBrowser(url); oerr != nil {
					log.Printf("open browser: %v — visit %s", oerr, url)
				}
			}
			return
		}
		log.Fatal(err)
	}

	log.Printf("MechMind bay UI on %s (USB stays in this process; browser is display only)", url)
	log.Printf("API target %s", *apiURL)

	if *open {
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := openBrowser(url); err != nil {
				log.Printf("open browser: %v — visit %s", err, url)
			}
		}()
	}

	srv := &http.Server{Handler: mux}
	bay.srv = srv
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	log.Printf("Bay stopped")
}

func addrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	return strings.Contains(err.Error(), "address already in use")
}

func setupLog() {
	dir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "mechmind", "bayui.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

func openBrowser(url string) error {
	type cand struct {
		name string
		args []string
	}
	var cands []cand
	switch runtime.GOOS {
	case "windows":
		cands = []cand{{"cmd", []string{"/c", "start", "", url}}}
	case "darwin":
		cands = []cand{{"open", []string{url}}}
	default:
		cands = []cand{
			{"xdg-open", []string{url}},
			{"gio", []string{"open", url}},
			{"firefox", []string{url}},
			{"google-chrome", []string{url}},
			{"chromium", []string{url}},
		}
	}
	var last error
	for _, c := range cands {
		if _, err := exec.LookPath(c.name); err != nil {
			continue
		}
		cmd := exec.Command(c.name, c.args...)
		cmd.Stdout = nil
		cmd.Stderr = nil
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			last = err
			continue
		}
		_ = cmd.Process.Release()
		log.Printf("opened %s via %s", url, c.name)
		return nil
	}
	if last == nil {
		last = fmt.Errorf("no browser opener found")
	}
	return last
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}
