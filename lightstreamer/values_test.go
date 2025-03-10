package lightstreamer

import (
	"strconv"
	"strings"
	"testing"
)

func TestValues_String(t *testing.T) {
	tests := []struct {
		name string
		v    Values
		want string
	}{
		{"populated", Values{valuePtr("1"), nil, valuePtr("3")}, "1,<nil>,3"},
		{"empty", Values{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.String(); got != tt.want {
				t.Errorf("Values.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValues_Update(t *testing.T) {
	tests := []struct {
		name    string
		current string
		updated string
		pass    bool
		want    string
	}{
		{"all new values", "1|2|3", "4|5|6", true, "4,5,6"},
		{"blank: no change", "1|2|3", "4||6", true, "4,2,6"},
		{"hash sign: value is null", "1|2|3", "4|#|6", true, "4,<nil>,6"},
		{"dollar sign: value is blank", "1|2|3", "4|$|6", true, "4,,6"},
		{"blank to non-blank", "1|#|3", "|$|", true, "1,,3"},
		{"skip fields", "1|2|3|4", "^3|5", true, "1,2,3,5"},
		{"encoded string", "foo%20bar", "", true, "foo bar"},
		{"update too many values", "1|2", "1|2|3", false, ""},
		{"update not enough values", "1|2|3", "1|2", false, ""},
		{"skip too far", "1|2|3", "^6|4", false, ""},
		{"skip invalid", "1|2|3", "^A|4", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current, err := Values{}.Update(strings.Split(tt.current, "|"))
			if err != nil {
				t.Fatalf("Values.Update(tt.current) error = %v", err)
			}
			updated, err := current.Update(strings.Split(tt.updated, "|"))
			if tt.pass != (err == nil) {
				t.Fatalf("Values.Update(tt.current) error = %v", err)
			}
			if got := updated.String(); got != tt.want {
				t.Errorf("Values.Update() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Before:
// BenchmarkValues_Update/orig-16         	  670264	      1636 ns/op	    8192 B/op	       1 allocs/op
// Current:
// BenchmarkValues_Update/current-16      	 2432552	       493.0 ns/op	       0 B/op	       0 allocs/op
func BenchmarkValues_Update(b *testing.B) {
	const size = 1_000
	orig := make(Values, size)
	update := make([]string, size)
	for i := range size {
		value := Value(strconv.Itoa(i))
		orig[i] = &value
		update[i] = ""
	}
	b.Run("current", func(b *testing.B) {
		b.ReportAllocs()
		var err error
		var next Values
		for b.Loop() {
			next, err = orig.Update(update)
			if err != nil {
				b.Fatalf("Values.Update() error = %v", err)
			}
		}
		if orig.String() != next.String() {
			b.Errorf("unexpected result")
		}
	})
}
