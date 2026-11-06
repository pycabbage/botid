package glob

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

const anyDepth = "**"

// Validate reports whether pattern is a well-formed pattern for Match.
// A pattern is a list of segments separated by "/". It may start with one "/".
func Validate(pattern string) error {
	if pattern == "" {
		return errors.New("pattern is empty")
	}
	segments := strings.Split(pattern, "/")
	for i, s := range segments {
		switch {
		case s == "" && i == 0 && len(segments) > 1:
		case s == "":
			return fmt.Errorf("pattern %q has an empty segment", pattern)
		case s == anyDepth:
		default:
			// path.Match reports a malformed pattern even when the name does not match.
			if _, err := path.Match(s, ""); err != nil {
				return fmt.Errorf("pattern %q: segment %q: %w", pattern, s, err)
			}
		}
	}
	return nil
}

// Match reports whether name matches pattern. The caller has validated pattern with Validate.
//
// Both sides are split on "/". A segment "**" matches zero or more segments, except
// as the last segment, where it matches one or more. Any other segment is matched with
// path.Match, so "*" and "?" never match "/". An empty name matches nothing.
func Match(pattern, name string) bool {
	if name == "" {
		return false
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pattern, name []string) bool {
	if len(pattern) == 0 {
		return len(name) == 0
	}
	if pattern[0] == anyDepth {
		if len(pattern) == 1 {
			return len(name) > 0
		}
		for i := range len(name) + 1 {
			if matchSegments(pattern[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	ok, err := path.Match(pattern[0], name[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pattern[1:], name[1:])
}
