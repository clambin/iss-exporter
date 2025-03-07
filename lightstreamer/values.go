package lightstreamer

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type Value string
type Values []*Value

func (v Values) String() string {
	s := make([]string, len(v))
	for i := range v {
		if v[i] != nil {
			s[i] = string(*v[i])
		} else {
			s[i] = "<nil>"
		}
	}
	return strings.Join(s, ",")
}

func (v Values) Update(values []string) (Values, error) {
	if len(v) == 0 {
		v = make(Values, len(values))
	}

	// don't really need to make a copy, since Update() operates on a copy of v
	next := make(Values, len(v))
	copy(next, v)

	var idx int
	for _, value := range values {
		if idx > len(v)-1 {
			return Values{}, errors.New("invalid value")
		}
		switch {
		case value == "":
		case value == "#":
			next[idx] = nil
		case value == "$":
			next[idx] = valuePtr("")
		case value[0] == '^':
			step, err := strconv.Atoi(value[1:])
			if err != nil {
				return Values{}, fmt.Errorf("invalid step value: %w", err)
			}
			idx += step - 1
		default:
			if v2, err := url.PathUnescape(value); err == nil {
				value = v2
			}
			next[idx] = valuePtr(value)
		}
		idx++
	}
	if idx != len(v) {
		return Values{}, errors.New("not enough values in update")
	}

	return next, nil
}

func valuePtr(v string) *Value {
	vv := Value(v)
	return &vv
}
