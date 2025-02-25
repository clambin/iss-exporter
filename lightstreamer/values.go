package lightstreamer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Values represents a set of values received from the server for a subscription.
//
// Notes:
//   - Values is implemented as a slice of strings, meaning null and empty fields are indistinguishable. The "#" indicator is therefore equivalent to "$".
//   - Percent-encoding values are currently not supported.
type Values []string

// String returns a comma-separated string representation of Values.
func (v Values) String() string {
	return strings.Join(v, ",")
}

// Update updates the current Values with newly received Values.
func (v Values) Update(update Values) (Values, error) {
	if len(v) == 0 {
		return update, nil
	}

	// don't really need to make a copy, since update() operates on a copy of v
	next := make(Values, len(v))
	copy(next, v)
	var idx int
	for _, value := range update {
		if idx > len(v)-1 {
			return Values{}, errors.New("invalid value")
		}
		switch {
		case value == "":
		case value == "#" || value == "$":
			next[idx] = ""
		case value[0] == '^':
			step, err := strconv.Atoi(value[1:])
			if err != nil {
				return Values{}, fmt.Errorf("invalid step value: %w", err)
			}
			idx += step - 1
		default:
			// TODO: percent-decode string. url.XXXUnescape?
			next[idx] = value
		}
		idx++
	}
	if idx != len(v) {
		return Values{}, errors.New("not enough values in update")
	}

	return next, nil
}
