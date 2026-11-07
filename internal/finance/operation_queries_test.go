package finance

import "testing"

func TestQueryDate(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, value string
		valid       bool
	}{{"civil", "2026-11-01", true}, {"leap", "2024-02-29", true}, {"invalid_day", "2026-02-29", false}, {"year_zero", "0000-01-01", false}, {"instant", "2026-11-01T00:00:00Z", false}, {"missing", "", false}} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := queryDate(tt.value, "")
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, error=%v", tt.valid, err)
			}
		})
	}
}

func TestQueryPage(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, cursor string
		limit        int32
		valid        bool
	}{{"first", "", 1, true}, {"max", "", 100, true}, {"zero", "", 0, false}, {"large", "", 101, false}, {"malformed", "invalid", 1, false}} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validatePage(tt.cursor, tt.limit)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, error=%v", tt.valid, err)
			}
		})
	}
}
