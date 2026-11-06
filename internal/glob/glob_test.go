package glob

import "testing"

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"github.com/octocorp/*", "github.com/octocorp/widget", true},
		{"github.com/octocorp/*", "github.com/octocorp/a/b", false},
		{"github.com/octocorp/*", "github.com/octocorp", false},
		{"github.com/*/anyproduct-*", "github.com/someone/anyproduct-x", true},
		{"github.com/*/anyproduct-*", "github.com/someone/other", false},
		{"github.com/*/anyproduct-*", "github.com/a/b/anyproduct-x", false},
		{"release/*", "release/1.0", true},
		{"release/*", "release/1.0/rc", false},
		{"release/**", "release/1.0", true},
		{"release/**", "release/1.0/rc", true},
		{"release/**", "release", false},
		{"**/foo", "foo", true},
		{"**/foo", "a/foo", true},
		{"**/foo", "a/b/foo", true},
		{"**/foo", "a/b/foobar", false},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/x/c", false},
		{"**", "a", true},
		{"**", "a/b", true},
		{"**", "", false},
		{"a**b", "axxb", true},
		{"a**b", "ab", true},
		{"a**b", "a/b", false},
		{"a?c", "abc", true},
		{"a?c", "a/c", false},
		{"[a-c]x", "bx", true},
		{"[a-c]x", "dx", false},
		{`a\*b`, "a*b", true},
		{`a\*b`, "axb", false},
		{"/home/me/**", "/home/me/x", true},
		{"/home/me/**", "/home/me/x/y", true},
		{"/home/me/**", "/home/me", false},
		{"/home/me/**", "/home/you/x", false},
		{"/**", "/home", true},
		{"/home/me", "/home/me", true},
		{"home/me", "/home/me", false},
		{"/home/me", "home/me", false},
		{"main", "main", true},
		{"main", "main2", false},
		{"*", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+" vs "+tt.name, func(t *testing.T) {
			if got := Match(tt.pattern, tt.name); got != tt.want {
				t.Errorf("Match(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	valid := []string{
		"a",
		"a/b",
		"**",
		"/**",
		"/home/me/**",
		"a/**/b",
		"a[a-z]b",
		`a\*b`,
		"a**b",
	}
	for _, p := range valid {
		t.Run("valid "+p, func(t *testing.T) {
			if err := Validate(p); err != nil {
				t.Errorf("Validate(%q) = %v, want nil", p, err)
			}
		})
	}

	invalid := []string{
		"",
		"a//b",
		"a/",
		"/",
		"//a",
		"[",
		"a[",
		"[a-",
		`a\`,
		"a/[/b",
	}
	for _, p := range invalid {
		t.Run("invalid "+p, func(t *testing.T) {
			if err := Validate(p); err == nil {
				t.Errorf("Validate(%q) = nil, want error", p)
			}
		})
	}
}
