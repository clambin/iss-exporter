package v2

import (
	"strings"
	"testing"
)

func TestValues_String(t *testing.T) {
	tests := []struct {
		name string
		v    Values
		want string
	}{
		{
			name: "populated",
			v:    Values{valuePtr("1"), nil, valuePtr("3")},
			want: "1,<nil>,3",
		},
		{
			name: "empty",
			v:    Values{},
			want: "",
		},
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
