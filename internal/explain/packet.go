package explain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/autoservice/autoservice/internal/diagnosis"
	"github.com/autoservice/autoservice/internal/store"
)

// Packet is the only structured context meant for an LLM call.
// It is intentionally sparse: rules/DB already did the heavy lifting.
type Packet struct {
	Code     string          `json:"code"`
	Vehicle  string          `json:"vehicle,omitempty"`
	Findings []PacketFinding `json:"findings,omitempty"`
	KB       *PacketKB       `json:"kb,omitempty"`
	CoOccur  []PacketCoOccur `json:"co_occur,omitempty"`
	History  []string        `json:"history,omitempty"`
	Ask      string          `json:"ask"`
}

type PacketFinding struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Confidence float64  `json:"confidence"`
	Evidence   []string `json:"evidence,omitempty"`
}

type PacketKB struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Causes  []string `json:"causes,omitempty"`
	Tests   []string `json:"tests,omitempty"`
	Parts   []string `json:"parts,omitempty"`
}

type PacketCoOccur struct {
	Code string  `json:"code"`
	Rate float64 `json:"rate"`
}

// BuildInput is everything available after MechMind's own analysis.
type BuildInput struct {
	Code     string
	Findings []diagnosis.Finding
	Article  *store.KnowledgeArticle
	Vehicle  *store.VehicleEnrichment
	CoOccur  []store.CoOccurRow
	History  []store.HistoryHit
}

// BuildPacket keeps only what a technician narrative needs.
// Selection is structural (rank/cut), not a token counter.
func BuildPacket(in BuildInput) Packet {
	p := Packet{
		Code: strings.ToUpper(strings.TrimSpace(in.Code)),
		Ask:  "Write a short bay triage (5–8 sentences) for a technician. Use only CONTEXT. Cite finding ids. Do not invent DTCs, parts, or measurements.",
	}
	if in.Vehicle != nil {
		parts := []string{}
		if in.Vehicle.Make != "" {
			parts = append(parts, in.Vehicle.Make)
		}
		if in.Vehicle.Model != "" {
			parts = append(parts, in.Vehicle.Model)
		}
		if in.Vehicle.Year > 0 {
			parts = append(parts, fmt.Sprintf("%d", in.Vehicle.Year))
		}
		p.Vehicle = strings.Join(parts, " ")
	}

	// Top findings only; trim evidence to what rules already highlighted.
	for i, f := range in.Findings {
		if i >= 3 {
			break
		}
		ev := f.Evidence
		if len(ev) > 2 {
			ev = ev[:2]
		}
		p.Findings = append(p.Findings, PacketFinding{
			ID:         f.ID,
			Title:      f.Title,
			Confidence: f.Confidence,
			Evidence:   ev,
		})
	}

	if in.Article != nil {
		p.KB = &PacketKB{
			Title:   in.Article.Title,
			Summary: clipSentence(in.Article.Summary, 280),
			Causes:  take(in.Article.LikelyCauses, 3),
			Tests:   take(in.Article.Tests, 3),
			Parts:   take(in.Article.Parts, 3),
		}
	}

	for _, c := range in.CoOccur {
		if c.Rate < 0.25 {
			continue
		}
		p.CoOccur = append(p.CoOccur, PacketCoOccur{Code: c.Code, Rate: round2(c.Rate)})
		if len(p.CoOccur) >= 2 {
			break
		}
	}

	for i, h := range in.History {
		if i >= 2 {
			break
		}
		p.History = append(p.History, fmt.Sprintf("%s %s", h.StartedAt.UTC().Format("2006-01-02"), h.Status))
	}
	return p
}

// WorthNarrating decides whether an LLM call adds value.
// High-confidence single finding + KB can be shown as structured UI without prose.
func WorthNarrating(p Packet, force bool) (bool, string) {
	if force {
		return true, "forced"
	}
	if len(p.Findings) == 0 && p.KB == nil {
		return false, "no_findings_or_kb"
	}
	if len(p.Findings) == 1 && p.Findings[0].Confidence >= 85 && p.KB != nil {
		return false, "structured_sufficient"
	}
	if len(p.Findings) == 0 && p.KB != nil {
		// KB-only dictionary prose is low value for LLM.
		return false, "kb_only"
	}
	return true, "ok"
}

// Fingerprint is a stable cache key for identical LLM context.
func (p Packet) Fingerprint() string {
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// SystemPrompt is stable (cache-friendly with providers that support prompt caching).
func SystemPrompt() string {
	return strings.TrimSpace(`
You are MechMind, a technician triage assistant.
Use ONLY the JSON CONTEXT the user provides.
If CONTEXT is insufficient, say so briefly.
Cite finding ids in parentheses when you rely on them.
Never invent DTCs, sensor values, or parts not present in CONTEXT.
Keep the answer concise for a busy bay.
`)
}

// UserPrompt wraps the lean packet.
func UserPrompt(p Packet) string {
	b, _ := json.Marshal(p)
	return "CONTEXT:\n" + string(b)
}

func take(in []string, n int) []string {
	if len(in) <= n {
		return in
	}
	return in[:n]
}

func clipSentence(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	cut := strings.LastIndex(s[:max], ". ")
	if cut > max/2 {
		return s[:cut+1]
	}
	return s[:max-1] + "…"
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// CacheEntry stores a prior narrative for identical packets.
type CacheEntry struct {
	Text      string
	Model     string
	CreatedAt time.Time
}
