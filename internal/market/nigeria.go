package market

// Nigeria fleet focus: 2010+ vehicles with denser electronics, top makes on Nigerian roads
// (tokunbo-heavy fleet) and recent sales mix.
const (
	RegionCode    = "NG"
	MinModelYear  = 2010
	MaxSeedYear   = 2024
	SeedYearStep  = 2 // populate recalls for even years only — enough triage coverage
)

// TopMakes are the five focus brands for this deployment.
var TopMakes = []string{
	"Toyota",
	"Honda",
	"Hyundai",
	"Kia",
	"Mercedes-Benz",
}

func IsTopMake(make string) bool {
	for _, m := range TopMakes {
		if equalFoldTrim(m, make) {
			return true
		}
	}
	return false
}

func InYearScope(year int) bool {
	return year >= MinModelYear
}

func InMarketScope(make string, year int) bool {
	return IsTopMake(make) && InYearScope(year)
}

func equalFoldTrim(a, b string) bool {
	return normalize(a) == normalize(b)
}

func normalize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '-' || c == '_' {
			continue
		}
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}
