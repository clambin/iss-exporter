package lightstreamer

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestValues_Update(t *testing.T) {
	tests := []struct {
		name    string
		current Values
		update  Values
		err     assert.ErrorAssertionFunc
		want    Values
	}{
		{
			name:   "init",
			update: Values{"1", "2", "3", "4"},
			err:    assert.NoError,
			want:   Values{"1", "2", "3", "4"},
		},
		{
			name:    "blank field maintains the value",
			current: Values{"1", "2", "3", "4"},
			update:  Values{"1", "3", "", "2"},
			err:     assert.NoError,
			want:    Values{"1", "3", "3", "2"},
		},
		{
			name:    "$ means a nil value",
			current: Values{"1", "2", "3", "4"},
			update:  Values{"1", "3", "$", "4"},
			err:     assert.NoError,
			want:    Values{"1", "3", "", "4"},
		},
		{
			name:    "^n skips n cells",
			current: Values{"1", "2", "3", "4"},
			update:  Values{"1", "^2", "5"},
			err:     assert.NoError,
			want:    Values{"1", "2", "3", "5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, err := tt.current.Update(tt.update)
			tt.err(t, err)
			assert.Equal(t, tt.want, next)
		})
	}
}
