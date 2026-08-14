package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/autoservice/autoservice/pkg/obd"
)

type bayServer struct {
	mu     sync.Mutex
	apiURL string
	token  string
	http   *http.Client

	jobMu sync.Mutex
	job   *scanJob
}

type scanJob struct {
	mu      sync.Mutex
	busy    bool
	done    bool
	phase   string
	log     []string
	started time.Time
	timeout time.Duration
	err     string
	result  map[string]any
	cancel  context.CancelFunc
}

func (b *bayServer) serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	raw, err := fs.ReadFile(webFS, "web/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

func (b *bayServer) handleState(w http.ResponseWriter, r *http.Request) {
	devs, _ := obd.ListUSBSerialDevices()
	b.mu.Lock()
	apiURL := b.apiURL
	tok := b.token
	b.mu.Unlock()

	st := b.jobStatus()
	out := map[string]any{
		"api_url":   apiURL,
		"devices":   devs,
		"busy":      st["busy"],
		"logged_in": tok != "",
		"scan":      st,
	}

	if tok != "" {
		if me, err := b.apiJSON(r.Context(), http.MethodGet, "/v1/auth/me", nil); err == nil {
			out["technician"] = me
			out["api_ok"] = true
		} else {
			out["api_ok"] = false
			out["api_error"] = err.Error()
		}
	} else {
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(apiURL, "/")+"/healthz", nil)
		resp, err := b.http.Do(req)
		if err != nil {
			out["api_ok"] = false
			out["api_error"] = err.Error()
		} else {
			resp.Body.Close()
			out["api_ok"] = resp.StatusCode < 300
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (b *bayServer) handleDevices(w http.ResponseWriter, _ *http.Request) {
	devs, err := obd.ListUSBSerialDevices()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs})
}

func (b *bayServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIURL   string `json:"api_url"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.APIURL) != "" {
		b.mu.Lock()
		b.apiURL = strings.TrimRight(req.APIURL, "/")
		b.mu.Unlock()
	}
	tok, tech, err := b.login(r.Context(), req.Email, req.Password)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	b.mu.Lock()
	b.token = tok
	b.mu.Unlock()
	_ = saveToken(tok)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "technician": tech})
}

func (b *bayServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIURL      string `json:"api_url"`
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		ShopName    string `json:"shop_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.APIURL) != "" {
		b.mu.Lock()
		b.apiURL = strings.TrimRight(req.APIURL, "/")
		b.mu.Unlock()
	}
	body := map[string]string{
		"email":        req.Email,
		"password":     req.Password,
		"display_name": req.DisplayName,
		"shop_name":    req.ShopName,
	}
	out, err := b.apiJSON(r.Context(), http.MethodPost, "/v1/auth/register", body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	tok, _ := out["token"].(string)
	if tok == "" {
		writeErr(w, http.StatusBadRequest, "register response missing token")
		return
	}
	b.mu.Lock()
	b.token = tok
	b.mu.Unlock()
	_ = saveToken(tok)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "technician": out["technician"], "organization": out["organization"]})
}

func (b *bayServer) handleLogout(w http.ResponseWriter, _ *http.Request) {
	b.mu.Lock()
	b.token = ""
	b.mu.Unlock()
	_ = os.Remove(tokenPath())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (b *bayServer) handleScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mock   bool   `json:"mock"`
		Device string `json:"device"`
		Baud   int    `json:"baud"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	b.mu.Lock()
	tok := b.token
	b.mu.Unlock()
	if tok == "" {
		writeErr(w, http.StatusUnauthorized, "log in first — use a shop technician account, not the bootstrap super_admin")
		return
	}

	adapter, err := b.buildAdapter(req.Mock, req.Device, req.Baud, b.note)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	const budget = 20 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)

	b.jobMu.Lock()
	busy := false
	if b.job != nil {
		b.job.mu.Lock()
		busy = b.job.busy
		b.job.mu.Unlock()
	}
	if busy {
		b.jobMu.Unlock()
		cancel()
		writeErr(w, http.StatusConflict, "a scan is already running")
		return
	}
	job := &scanJob{
		busy:    true,
		phase:   "Starting",
		started: time.Now(),
		timeout: budget,
		cancel:  cancel,
		log:     []string{time.Now().Format("15:04:05") + "  Starting"},
	}
	b.job = job
	b.jobMu.Unlock()

	go b.runScan(ctx, cancel, job, adapter, req.Mock)
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "timeout_ms": budget.Milliseconds()})
}

func (b *bayServer) handleScanStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, b.jobStatus())
}

func (b *bayServer) handleScanCancel(w http.ResponseWriter, _ *http.Request) {
	b.jobMu.Lock()
	job := b.job
	b.jobMu.Unlock()
	if job == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cancelled": false})
		return
	}
	job.mu.Lock()
	busy := job.busy
	cancel := job.cancel
	job.mu.Unlock()
	if !busy {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cancelled": false})
		return
	}
	if cancel != nil {
		cancel()
	}
	b.note("Cancel requested")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cancelled": true})
}

func (b *bayServer) note(phase string) {
	b.jobMu.Lock()
	job := b.job
	b.jobMu.Unlock()
	if job == nil {
		return
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	job.phase = phase
	job.log = append(job.log, time.Now().Format("15:04:05")+"  "+phase)
	if len(job.log) > 50 {
		job.log = job.log[len(job.log)-50:]
	}
}

func (b *bayServer) jobStatus() map[string]any {
	b.jobMu.Lock()
	job := b.job
	b.jobMu.Unlock()
	out := map[string]any{
		"busy":       false,
		"done":       false,
		"phase":      "",
		"log":        []string{},
		"elapsed_ms": 0,
		"timeout_ms": 20000,
	}
	if job == nil {
		return out
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	elapsed := time.Since(job.started)
	out["busy"] = job.busy
	out["done"] = job.done
	out["phase"] = job.phase
	out["log"] = append([]string{}, job.log...)
	out["elapsed_ms"] = elapsed.Milliseconds()
	out["timeout_ms"] = job.timeout.Milliseconds()
	if job.err != "" {
		out["error"] = job.err
	}
	if job.result != nil {
		out["result"] = job.result
	}
	return out
}

func (b *bayServer) finishJob(job *scanJob, result map[string]any, err error) {
	job.mu.Lock()
	job.busy = false
	job.done = true
	if err != nil {
		job.err = err.Error()
		job.phase = "Failed"
	} else {
		job.phase = "Done"
		job.result = result
	}
	job.mu.Unlock()
}

func (b *bayServer) runScan(ctx context.Context, cancel context.CancelFunc, job *scanJob, adapter obd.Adapter, mock bool) {
	defer cancel()
	scanner := &obd.Scanner{Adapter: adapter, Progress: b.note}
	result, err := scanner.Scan(ctx)
	if err != nil {
		phase := "unknown step"
		job.mu.Lock()
		if job.phase != "" {
			phase = job.phase
		}
		job.mu.Unlock()
		if ctx.Err() != nil {
			err = fmt.Errorf("stopped at “%s” (%v). Ignition ON, adapter seated, correct port/baud?", phase, ctx.Err())
		}
		b.note(err.Error())
		b.finishJob(job, nil, err)
		return
	}
	if result.Vehicle.VIN == "" {
		err := fmt.Errorf("ECU did not return a VIN — check ignition ON, adapter seating, and serial permissions")
		b.note(err.Error())
		b.finishJob(job, nil, err)
		return
	}
	if strings.HasPrefix(result.Vehicle.VIN, "MOCK") && !mock {
		err := fmt.Errorf("refusing mock VIN on a live adapter")
		b.finishJob(job, nil, err)
		return
	}

	b.note("Uploading scan to MechMind")
	upload, uerr := b.uploadScan(ctx, result)
	out := map[string]any{"scan": result, "explanations": []any{}}
	if uerr != nil {
		out["upload_error"] = uerr.Error()
		b.note("Upload failed: " + uerr.Error())
		b.finishJob(job, out, nil)
		return
	}
	out["upload"] = upload

	exps := make([]map[string]any, 0, len(result.DTCs))
	for _, d := range result.DTCs {
		b.note("Explaining " + d.Code)
		exp, eerr := b.explain(context.Background(), d.Code, result.Vehicle.VIN, false)
		if eerr != nil {
			exps = append(exps, map[string]any{"code": d.Code, "error": eerr.Error()})
			continue
		}
		exp["code"] = d.Code
		exps = append(exps, exp)
	}
	out["explanations"] = exps
	b.note("Done")
	b.finishJob(job, out, nil)
}

func (b *bayServer) handleExplain(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	vin := r.URL.Query().Get("vin")
	narr := r.URL.Query().Get("narrative") == "1"
	if code == "" {
		writeErr(w, http.StatusBadRequest, "code required")
		return
	}
	exp, err := b.explain(r.Context(), code, vin, narr)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, exp)
}

func (b *bayServer) buildAdapter(mock bool, device string, baud int, progress func(string)) (obd.Adapter, error) {
	if mock {
		return obd.NewMockAdapter("MOCKTESTVIN000001", []string{"P0300", "P0171"}), nil
	}
	path := strings.TrimSpace(device)
	if path == "" {
		devs, err := obd.ListUSBSerialDevices()
		if err != nil {
			return nil, err
		}
		if len(devs) == 0 {
			return nil, fmt.Errorf("no USB serial device found — plug in the ELM327, or use mock mode")
		}
		path = devs[0]
	}
	usb := obd.NewUSBAdapter(path)
	if baud > 0 {
		usb.Baud = baud
	}
	usb.Progress = progress
	return usb, nil
}

func (b *bayServer) login(ctx context.Context, email, password string) (string, any, error) {
	out, err := b.apiJSON(ctx, http.MethodPost, "/v1/auth/login", map[string]string{
		"email": email, "password": password,
	})
	if err != nil {
		return "", nil, err
	}
	tok, _ := out["token"].(string)
	if tok == "" {
		return "", nil, fmt.Errorf("login response missing token")
	}
	return tok, out["technician"], nil
}

func (b *bayServer) uploadScan(ctx context.Context, result *obd.ScanResult) (map[string]any, error) {
	return b.apiJSON(ctx, http.MethodPost, "/v1/scans", map[string]any{
		"vin":          result.Vehicle.VIN,
		"link_type":    result.LinkType,
		"adapter_name": result.AdapterName,
		"protocol":     result.Protocol,
		"dtcs":         result.DTCs,
		"observations": result.Observations,
	})
}

func (b *bayServer) explain(ctx context.Context, code, vin string, narrative bool) (map[string]any, error) {
	path := "/v1/codes/" + url.PathEscape(code) + "/explain?vin=" + url.QueryEscape(vin)
	if narrative {
		path += "&narrative=1"
	}
	return b.apiJSON(ctx, http.MethodGet, path, nil)
}

func (b *bayServer) apiJSON(ctx context.Context, method, path string, body any) (map[string]any, error) {
	b.mu.Lock()
	base := b.apiURL
	tok := b.token
	b.mu.Unlock()

	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok != "" && !strings.HasPrefix(path, "/v1/auth/login") && !strings.HasPrefix(path, "/v1/auth/register") {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API unreachable at %s: %w", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s", apiErrBody(raw))
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func tokenPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "mechmind", "token")
}

func saveToken(tok string) error {
	path := tokenPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(tok), 0o600)
}

func loadSavedToken() string {
	b, err := os.ReadFile(tokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func apiErrBody(raw []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "request failed"
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}
