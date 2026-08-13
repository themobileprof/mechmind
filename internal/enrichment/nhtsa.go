package enrichment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultVPICBase = "https://vpic.nhtsa.dot.gov/api"
	defaultNHTSABase = "https://api.nhtsa.gov"
)

// LeanVIN is the triage subset we keep from vPIC — not the full Results payload.
type LeanVIN struct {
	Make           string
	Model          string
	Year           int
	BodyClass      string
	FuelType       string
	DisplacementL  string
	Cylinders      string
	DriveType      string
	EngineNote     string
	ErrorCode      string
	ErrorText      string
}

// LeanRecall is enough to surface a campaign during fault triage.
type LeanRecall struct {
	CampaignNumber string
	Component      string
	Summary        string
	Consequence    string
	Remedy         string
	ReportDate     string
}

type Client struct {
	HTTP      *http.Client
	VPICBase  string
	NHTSABase string
}

func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: 20 * time.Second,
		},
		VPICBase:  defaultVPICBase,
		NHTSABase: defaultNHTSABase,
	}
}

func (c *Client) DecodeVIN(ctx context.Context, vin string) (*LeanVIN, error) {
	vin = strings.ToUpper(strings.TrimSpace(vin))
	u := fmt.Sprintf("%s/vehicles/DecodeVinValues/%s?format=json", strings.TrimRight(c.VPICBase, "/"), url.PathEscape(vin))
	var raw struct {
		Results []map[string]any `json:"Results"`
	}
	if err := c.getJSON(ctx, u, &raw); err != nil {
		return nil, err
	}
	if len(raw.Results) == 0 {
		return nil, fmt.Errorf("vpic returned no results")
	}
	r := raw.Results[0]
	year := atoi(str(r["ModelYear"]))
	lean := &LeanVIN{
		Make:          str(r["Make"]),
		Model:         str(r["Model"]),
		Year:          year,
		BodyClass:     str(r["BodyClass"]),
		FuelType:      str(r["FuelTypePrimary"]),
		DisplacementL: str(r["DisplacementL"]),
		Cylinders:     str(r["EngineCylinders"]),
		DriveType:     str(r["DriveType"]),
		EngineNote:    trimJoin(str(r["EngineConfiguration"]), str(r["EngineModel"]), str(r["ValveTrainDesign"])),
		ErrorCode:     str(r["ErrorCode"]),
		ErrorText:     str(r["ErrorText"]),
	}
	return lean, nil
}

func (c *Client) RecallsByMMY(ctx context.Context, makeName, modelName string, year int) ([]LeanRecall, error) {
	q := url.Values{}
	q.Set("make", makeName)
	q.Set("model", modelName)
	q.Set("modelYear", fmt.Sprintf("%d", year))
	u := fmt.Sprintf("%s/recalls/recallsByVehicle?%s", strings.TrimRight(c.NHTSABase, "/"), q.Encode())

	var raw struct {
		Count   int              `json:"Count"`
		Results []map[string]any `json:"results"`
	}
	// NHTSA sometimes uses Results vs results — decode flexibly.
	body, err := c.getBytes(ctx, u)
	if err != nil {
		// NHTSA often returns HTTP 400 with Count=0 when no recalls exist.
		if body != nil && (strings.Contains(string(body), `"Count":0`) || strings.Contains(string(body), `"results":[]`)) {
			return []LeanRecall{}, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if len(raw.Results) == 0 {
		var alt struct {
			Results []map[string]any `json:"Results"`
		}
		_ = json.Unmarshal(body, &alt)
		raw.Results = alt.Results
	}

	out := make([]LeanRecall, 0, len(raw.Results))
	for _, r := range raw.Results {
		camp := firstStr(r, "NHTSACampaignNumber", "nhtsaCampaignNumber", "CampaignNumber")
		if camp == "" {
			continue
		}
		out = append(out, LeanRecall{
			CampaignNumber: camp,
			Component:      truncate(firstStr(r, "Component", "component"), 120),
			Summary:        truncate(firstStr(r, "Summary", "summary"), 280),
			Consequence:    truncate(firstStr(r, "Conequence", "Consequence", "consequence"), 200), // NHTSA typo key exists historically
			Remedy:         truncate(firstStr(r, "Remedy", "remedy"), 200),
			ReportDate:     firstStr(r, "ReportReceivedDate", "reportReceivedDate"),
		})
	}
	return out, nil
}

func (c *Client) getJSON(ctx context.Context, u string, dest any) error {
	b, err := c.getBytes(ctx, u)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dest)
}

func (c *Client) getBytes(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2MiB cap
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return b, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	return b, nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		if t == float64(int(t)) {
			return fmt.Sprintf("%d", int(t))
		}
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := str(m[k]); v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func trimJoin(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}
