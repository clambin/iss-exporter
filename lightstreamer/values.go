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

	var idx int
	for _, value := range values {
		if idx > len(v)-1 {
			return Values{}, errors.New("too many values in update")
		}
		switch {
		case value == "":
		case value == "#":
			v[idx] = nil
		case value == "$":
			v[idx] = valuePtr("")
		case value[0] == '^':
			step, err := strconv.Atoi(value[1:])
			if err != nil {
				return Values{}, fmt.Errorf("invalid step value: %w", err)
			}
			idx += step - 1
		default:
			// don't unescape if we don't need to.
			if strings.IndexRune(value, '%') >= 0 {
				if v2, err := url.PathUnescape(value); err == nil {
					value = v2
				}
			}
			// don't change the value if we don't need to.
			if v[idx] == nil || *(v[idx]) != Value(value) {
				v[idx] = valuePtr(value)
			}
		}
		idx++
	}
	if idx != len(v) {
		return Values{}, errors.New("not enough values in update")
	}

	return v, nil
}

func valuePtr(v string) *Value {
	vv := Value(v)
	return &vv
}
