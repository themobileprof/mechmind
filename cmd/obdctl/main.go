package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/autoservice/autoservice/pkg/obd"
)

func main() {
	var (
		mock     = flag.Bool("mock", false, "use mock adapter (no hardware)")
		device   = flag.String("device", "", "USB serial device path (e.g. /dev/ttyUSB0)")
		baud     = flag.Int("baud", 38400, "serial baud rate (common: 9600, 38400, 115200)")
		list     = flag.Bool("list", false, "list candidate USB serial devices")
		vin      = flag.String("vin", "", "override / mock VIN")
		codes    = flag.String("codes", "P0300,P0171", "comma-separated mock DTC codes")
		apiURL   = flag.String("api", envOr("API_URL", "http://localhost:8080"), "API base URL (required)")
		email    = flag.String("email", envOr("AUTOSERVICE_EMAIL", ""), "technician email for login")
		password = flag.String("password", envOr("AUTOSERVICE_PASSWORD", ""), "technician password for login")
		token    = flag.String("token", "", "JWT bearer token (skips login if set)")
		loginOnly = flag.Bool("login", false, "login and save token, then exit")
		timeout  = flag.Duration("timeout", 20*time.Second, "scan timeout")
		noUpload = flag.Bool("no-upload", false, "scan only; do not upload (discouraged — API is the system of record)")
		allowVINOverride = flag.Bool("allow-vin-override", false, "permit --vin to replace ECU-reported VIN (never use when validating OBD hardware)")
	)
	flag.Parse()

	if *list {
		devs, err := obd.ListUSBSerialDevices()
		if err != nil {
			fatal(err)
		}
		if len(devs) == 0 {
			fmt.Println("No /dev/ttyUSB* or /dev/ttyACM* devices found.")
			return
		}
		for _, d := range devs {
			fmt.Println(d)
		}
		return
	}

	if strings.TrimSpace(*apiURL) == "" {
		fatal(fmt.Errorf("--api / API_URL is required"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout+30*time.Second)
	defer cancel()

	bearer := strings.TrimSpace(*token)
	if bearer == "" {
		bearer = loadSavedToken()
	}
	if *loginOnly || bearer == "" {
		if *email == "" || *password == "" {
			fatal(fmt.Errorf("login required: pass --email/--password (or AUTOSERVICE_EMAIL/PASSWORD), or --token"))
		}
		tok, err := apiLogin(ctx, *apiURL, *email, *password)
		if err != nil {
			fatal(err)
		}
		bearer = tok
		if err := saveToken(tok); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not save token:", err)
		}
		fmt.Fprintln(os.Stderr, "logged in; token saved to", tokenPath())
		if *loginOnly {
			return
		}
	}

	var adapter obd.Adapter
	switch {
	case *mock:
		mockVIN := "MOCKTESTVIN000001"
		if *vin != "" && *allowVINOverride {
			mockVIN = *vin
			fmt.Fprintln(os.Stderr, "warning: mock scan using overridden VIN", mockVIN)
		} else if *vin != "" {
			fmt.Fprintln(os.Stderr, "note: ignoring --vin on --mock (use --allow-vin-override to force); VIN will be MOCKTESTVIN000001")
		}
		adapter = obd.NewMockAdapter(mockVIN, splitCSV(*codes))
	default:
		path := *device
		if path == "" {
			devs, err := obd.ListUSBSerialDevices()
			if err != nil {
				fatal(err)
			}
			if len(devs) == 0 {
				fatal(fmt.Errorf("no USB serial device found; pass --device or use --mock"))
			}
			path = devs[0]
			fmt.Fprintf(os.Stderr, "using device %s\n", path)
		}
		usb := obd.NewUSBAdapter(path)
		usb.Baud = *baud
		adapter = usb
	}

	scanCtx, scanCancel := context.WithTimeout(ctx, *timeout)
	defer scanCancel()

	scanner := &obd.Scanner{Adapter: adapter}
	result, err := scanner.Scan(scanCtx)
	if err != nil {
		fatal(err)
	}
	if *vin != "" && result.Vehicle.VIN == "" {
		if !*mock && !*allowVINOverride {
			fatal(fmt.Errorf("ECU did not return a VIN; fix OBD link or pass --allow-vin-override (not for hardware validation)"))
		}
		result.Vehicle.VIN = *vin
	}
	if *vin != "" && result.Vehicle.VIN != "" && *vin != result.Vehicle.VIN {
		if !*allowVINOverride {
			fatal(fmt.Errorf("ECU VIN %s differs from --vin %s; omit --vin to trust the ECU, or pass --allow-vin-override", result.Vehicle.VIN, *vin))
		}
		fmt.Fprintf(os.Stderr, "warning: overriding ECU VIN %s with %s\n", result.Vehicle.VIN, *vin)
		result.Vehicle.VIN = *vin
	}
	if result.Vehicle.VIN == "" {
		fatal(fmt.Errorf("VIN missing from ECU; for mock use --mock (gets MOCKTESTVIN000001), for hardware fix the adapter link"))
	}
	if strings.HasPrefix(result.Vehicle.VIN, "MOCK") && !*mock {
		fatal(fmt.Errorf("refusing mock VIN on a non-mock adapter"))
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fatal(err)
	}

	if *noUpload {
		fmt.Fprintln(os.Stderr, "warning: --no-upload set; scan was not persisted to API")
		return
	}

	rec, err := uploadScan(ctx, *apiURL, bearer, result)
	if err != nil {
		fatal(err)
	}
	fmt.Fprintln(os.Stderr, "uploaded scan", rec["id"])

	for _, d := range result.DTCs {
		exp, err := explainCode(ctx, *apiURL, bearer, d.Code, result.Vehicle.VIN)
		if err != nil {
			fmt.Fprintln(os.Stderr, "explain", d.Code, ":", err)
			continue
		}
		fmt.Fprintf(os.Stderr, "--- explain %s ---\n", d.Code)
		_ = json.NewEncoder(os.Stderr).Encode(exp)
	}
}

func apiLogin(ctx context.Context, base, email, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("API unreachable at %s: %w", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("login failed (%d): %s", resp.StatusCode, raw)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		return "", fmt.Errorf("login response missing token")
	}
	return out.Token, nil
}

func uploadScan(ctx context.Context, base, token string, result *obd.ScanResult) (map[string]any, error) {
	body := map[string]any{
		"vin":           result.Vehicle.VIN,
		"link_type":     result.LinkType,
		"adapter_name":  result.AdapterName,
		"protocol":      result.Protocol,
		"dtcs":          result.DTCs,
		"observations":  result.Observations,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1/scans", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API unreachable at %s: %w", base, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upload failed (%d): %s", resp.StatusCode, respBody)
	}
	var out map[string]any
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func explainCode(ctx context.Context, base, token, code, vin string) (map[string]any, error) {
	url := fmt.Sprintf("%s/v1/codes/%s/explain?vin=%s", strings.TrimRight(base, "/"), code, vin)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s", raw)
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

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
