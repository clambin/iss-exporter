package lightstreamer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Values []string

func (v Values) String() string {
	return strings.Join(v, ",")
}

func (v Values) Update(update Values) (Values, error) {
	if len(v) == 0 {
		return update, nil
	}

	// don't really need to make a copy, since Update() operates on a copy of v
	next := make(Values, len(v))
	copy(next, v)
	var idx int
	for _, value := range update {
		if idx > len(v)-1 {
			return Values{}, errors.New("invalid value")
		}
		switch {
		case value == "":
		case value == "$":
			next[idx] = ""
		case value[0] == '^':
			step, err := strconv.Atoi(value[1:])
			if err != nil {
				return Values{}, fmt.Errorf("invalid step value: %w", err)
			}
			idx += step - 1
		default:
			next[idx] = value
		}
		idx++
	}
	if idx != len(v) {
		return Values{}, errors.New("not enough values in update")
	}

	return next, nil
}
