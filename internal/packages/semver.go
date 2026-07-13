package packages

import (
	"fmt"
	"strconv"
	"strings"
)

// MatchConstraint reports whether version satisfies a requires_byre-style
// constraint. Supported forms (no new dependency; deliberately small):
//
//	>=1.2.3   >1.2.3   <=1.2.3   <1.2.3   =1.2.3   1.2.3
//
// Versions are compared on the MAJOR.MINOR.PATCH numeric triple; a leading
// "v" is stripped; any "-prerelease" / "+build" suffix is ignored for the
// numeric compare (prerelease sorts as equal to the base patch -- sufficient
// for byre's own version stamps). Empty constraint matches everything.
func MatchConstraint(version, constraint string) (bool, error) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return true, nil
	}
	op, want, err := parseConstraint(constraint)
	if err != nil {
		return false, err
	}
	have, err := parseSemver(version)
	if err != nil {
		return false, fmt.Errorf("version %q: %w", version, err)
	}
	cmp := compareSemver(have, want)
	switch op {
	case ">=", "":
		return cmp >= 0, nil
	case ">":
		return cmp > 0, nil
	case "<=":
		return cmp <= 0, nil
	case "<":
		return cmp < 0, nil
	case "=", "==":
		return cmp == 0, nil
	default:
		return false, fmt.Errorf("unsupported constraint operator %q", op)
	}
}

func parseConstraint(c string) (op string, v [3]int, err error) {
	c = strings.TrimSpace(c)
	for _, candidate := range []string{">=", "<=", "==", ">", "<", "="} {
		if strings.HasPrefix(c, candidate) {
			rest := strings.TrimSpace(c[len(candidate):])
			v, err = parseSemver(rest)
			if err != nil {
				return "", v, fmt.Errorf("constraint %q: %w", c, err)
			}
			return candidate, v, nil
		}
	}
	// Bare version = equality.
	v, err = parseSemver(c)
	if err != nil {
		return "", v, fmt.Errorf("constraint %q: %w", c, err)
	}
	return "=", v, nil
}

func parseSemver(s string) ([3]int, error) {
	var out [3]int
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return out, fmt.Errorf("empty version")
	}
	// Drop prerelease / build metadata for the numeric triple.
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return out, fmt.Errorf("want MAJOR[.MINOR[.PATCH]], got %q", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("non-numeric component %q", p)
		}
		out[i] = n
	}
	return out, nil
}

func compareSemver(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
