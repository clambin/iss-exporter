package lightstreamer

import (
	"reflect"
	"testing"
)

func TestValues_Update(t *testing.T) {
	tests := []struct {
		name    string
		current Values
		update  Values
		pass    bool
		want    Values
	}{
		{
			name:   "init",
			update: Values{"1", "2", "3", "4"},
			pass:   true,
			want:   Values{"1", "2", "3", "4"},
		},
		{
			name:    "blank field maintains the value",
			current: Values{"1", "2", "3", "4"},
			update:  Values{"1", "3", "", "2"},
			pass:    true,
			want:    Values{"1", "3", "3", "2"},
		},
		{
			name:    "$ means a blank value",
			current: Values{"1", "2", "3", "4"},
			update:  Values{"1", "3", "$", "4"},
			pass:    true,
			want:    Values{"1", "3", "", "4"},
		},
		{
			name:    "^n skips n cells",
			current: Values{"1", "2", "3", "4"},
			update:  Values{"1", "^2", "5"},
			pass:    true,
			want:    Values{"1", "2", "3", "5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, err := tt.current.Update(tt.update)
			if tt.pass != (err == nil) {
				t.Errorf("expected pass %v but got %v", tt.pass, err)
			}
			if !reflect.DeepEqual(tt.want, next) {
				t.Errorf("expected %v but got %v", tt.want, next)
			}
		})
	}
}
