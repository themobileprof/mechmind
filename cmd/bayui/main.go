package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"
)

//go:embed web/*
var webFS embed.FS

func main() {
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
	mux.HandleFunc("GET /local/explain", bay.handleExplain)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + *addr + "/"
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

	if err := http.Serve(ln, mux); err != nil {
		log.Fatal(err)
	}
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
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
