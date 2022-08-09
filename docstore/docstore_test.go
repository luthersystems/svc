package docstore

import "testing"

func TestKeyValidation(t *testing.T) {
	var tests = []struct {
		key  string
		want bool
	}{
		{"abc", true},
		{"abc-1", true},
		{"..abc", true},
		{"..a(b)c.json", true}, // I wish we didn't support this
		{"a/b/c", true},
		{"", false},
		{"../abc", false},
		{"a/b/../c", false},
		{"/abc", false},
		{"//abc", false},
		{"abc 1", false},
		{"foo/../../../abc", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			ans := ValidKey(tt.key) == nil
			if ans != tt.want {
				t.Errorf("got %t, want %t", ans, tt.want)
			}
		})
	}
}
