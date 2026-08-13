package explain

import (
	"fmt"
	"testing"

	"github.com/autoservice/autoservice/internal/diagnosis"
	"github.com/autoservice/autoservice/internal/store"
)

func TestBuildPacketCutsFindingsAndEvidence(t *testing.T) {
	findings := make([]diagnosis.Finding, 0, 5)
	for i := 0; i < 5; i++ {
		findings = append(findings, diagnosis.Finding{
			ID:         fmt.Sprintf("f%d", i),
			Title:      "title",
			Confidence: float64(90 - i*5),
			Evidence:   []string{"e1", "e2", "e3", "e4"},
		})
	}
	p := BuildPacket(BuildInput{
		Code:     "P0171",
		Findings: findings,
		Article: &store.KnowledgeArticle{
			Title:        "Lean",
			Summary:      "Summary text for lean condition that should remain short enough for triage.",
			LikelyCauses: []string{"a", "b", "c", "d"},
			Tests:        []string{"t1", "t2", "t3", "t4"},
			Parts:        []string{"p1", "p2", "p3", "p4"},
		},
		CoOccur: []store.CoOccurRow{
			{Code: "P0300", Rate: 0.8},
			{Code: "P0420", Rate: 0.4},
			{Code: "P0001", Rate: 0.1},
		},
	})
	if len(p.Findings) != 3 {
		t.Fatalf("findings %d", len(p.Findings))
	}
	if len(p.Findings[0].Evidence) != 2 {
		t.Fatalf("evidence %d", len(p.Findings[0].Evidence))
	}
	if p.KB == nil || len(p.KB.Causes) != 3 || len(p.KB.Tests) != 3 {
		t.Fatalf("kb trim failed: %+v", p.KB)
	}
	if len(p.CoOccur) != 2 {
		t.Fatalf("cooccur %d", len(p.CoOccur))
	}
	// Raw observations must never appear on the packet type — ensured by struct fields.
}
