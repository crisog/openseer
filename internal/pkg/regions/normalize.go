package regions

import "strings"

// Normalize trims whitespace and lowercases region identifiers so worker- and monitor-supplied
// values consistently map to the same region bucket.
func Normalize(region string) string {
	normalized := strings.ToLower(strings.TrimSpace(region))
	if normalized == "" {
		return "global"
	}
	return normalized
}

// NormalizeList applies Normalize to each region and removes empty results.
func NormalizeList(regions []string) []string {
	if len(regions) == 0 {
		return regions
	}

	result := make([]string, 0, len(regions))
	seen := make(map[string]struct{}, len(regions))
	for _, region := range regions {
		n := Normalize(region)
		if _, exists := seen[n]; exists {
			continue
		}
		seen[n] = struct{}{}
		result = append(result, n)
	}
	return result
}
