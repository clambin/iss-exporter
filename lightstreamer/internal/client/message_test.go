package client

import (
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestParseSessionMessage(t *testing.T) {
	tests := []struct {
		name string
		line string
		pass bool
		want Message
	}{
		{
			name: "CONOK",
			line: "CONOK,sessionID,50000,5000,*",
			pass: true,
			want: Message{CONOKData{"sessionID", "*", 50000, 5000}, "CONOK"},
		},
		{
			name: "CONOK (too short)",
			line: "CONOK",
			pass: false,
		},
		{
			name: "CONOK (bad number)",
			line: "CONOK,sessionID,a,5000,*",
			pass: false,
		},
		{
			name: "CONOK (bad number)",
			line: "CONOK,sessionID,50000,a,*",
			pass: false,
		},
		{
			name: "SERVNAME",
			line: "SERVNAME,my server",
			pass: true,
			want: Message{SERVNAMEData{"my server"}, "SERVNAME"},
		},
		{
			name: "SERVNAME (too short)",
			line: "SERVNAME",
			pass: false,
		},
		{
			name: "CLIENTIP",
			line: "CLIENTIP,192.168.0.1",
			pass: true,
			want: Message{CLIENTIPData{"192.168.0.1"}, "CLIENTIP"},
		},
		{
			name: "CLIENTIP (too short)",
			line: "CLIENTIP",
			pass: false,
		},
		{
			name: "NOOP",
			line: "NOOP,ignored text",
			pass: true,
			want: Message{NOOPData{Preamble: []string{"ignored text"}}, "NOOP"},
		},
		{
			name: "CONS (unlimited)",
			line: "CONS,unlimited",
			pass: true,
			want: Message{CONSData{math.Inf(1)}, "CONS"},
		},
		{
			name: "CONS (limited)",
			line: "CONS,5000",
			pass: true,
			want: Message{CONSData{5000}, "CONS"},
		},
		{
			name: "CONS (too short)",
			line: "CONS",
			pass: false,
		},
		{
			name: "CONS (bad number)",
			line: "CONS,a",
			pass: false,
		},
		{
			name: "SYNC",
			line: "SYNC,5000",
			pass: true,
			want: Message{SYNCData{5000}, "SYNC"},
		},
		{
			name: "SYNC (bad number)",
			line: "SYNC,a",
			pass: false,
		},
		{
			name: "SYNC (too short)",
			line: "SYNC",
			pass: false,
		},
		{
			name: "PROBE",
			line: "PROBE",
			pass: true,
			want: Message{nil, "PROBE"},
		},
		{
			name: "LOOP",
			line: "LOOP,0",
			pass: true,
			want: Message{LOOPData{ExpectedDelay: 0}, "LOOP"},
		},
		{
			name: "LOOP (too short)",
			line: "LOOP",
			pass: false,
		},
		{
			name: "LOOP (bad number)",
			line: "LOOP,a",
			pass: false,
		},
		{
			name: "END",
			line: "END,10,done",
			pass: true,
			want: Message{ENDData{"done", 10}, "END"},
		},
		{
			name: "END (too short)",
			line: "END",
			pass: false,
		},
		{
			name: "END (bad number)",
			line: "END,a,error",
			pass: false,
		},
		{
			name: "U",
			line: "U,100,1,1,2,3",
			pass: true,
			want: Message{UData{[]string{"1", "2", "3"}, 100, 1}, "U"},
		},
		{
			name: "U (no data)",
			line: "U,100,1",
			pass: true,
			want: Message{UData{[]string{}, 100, 1}, "U"},
		},
		{
			name: "U (too short)",
			line: "U",
			pass: false,
		},
		{
			name: "U (invalid subscription ID)",
			line: "U,a,1",
			pass: false,
		},
		{
			name: "U (invalid item number)",
			line: "U,100,a",
			pass: false,
		},
		{
			name: "SUBOK",
			line: "SUBOK,100,1,5",
			pass: true,
			want: Message{SUBOKData{100, 1, 5}, "SUBOK"},
		},
		{
			name: "SUBOK (too short)",
			line: "SUBOK",
			pass: false,
		},
		{
			name: "SUBOK (invalid subscription ID)",
			line: "SUBOK,a,1,5",
			pass: false,
		},
		{
			name: "SUBOK (invalid items)",
			line: "SUBOK,1,a,5",
			pass: false,
		},
		{
			name: "SUBOK (invalid fields)",
			line: "SUBOK,1,1,a",
			pass: false,
		},
		{
			name: "CONF (filtered)",
			line: "CONF,100,100,filtered",
			pass: true,
			want: Message{CONFData{100, 100, true}, "CONF"},
		},
		{
			name: "CONF (unfiltered)",
			line: "CONF,100,100,unfiltered",
			pass: true,
			want: Message{CONFData{100, 100, false}, "CONF"},
		},
		{
			name: "CONF (unlimited)",
			line: "CONF,100,unlimited,unfiltered",
			pass: true,
			want: Message{CONFData{100, math.Inf(1), false}, "CONF"},
		},
		{
			name: "CONF (too short)",
			line: "CONF",
			pass: false,
		},
		{
			name: "CONF (invalid subscription ID)",
			line: "CONF,a,unlimited,unfiltered",
			pass: false,
		},
		{
			name: "CONF (invalid bandwidth)",
			line: "CONF,100,a,unfiltered",
			pass: false,
		},
		{
			name: "CONF (invalid filter)",
			line: "CONF,100,unlimited,a",
			pass: false,
		},
		{
			name: "PROG",
			line: "PROG,100",
			pass: true,
			want: Message{PROGData{100}, "PROG"},
		},
		{
			name: "PROG (too short)",
			line: "PROG",
			pass: false,
		},
		{
			name: "PROG (invalid number)",
			line: "PROG,a",
			pass: false,
		},
		{
			name: "unsupported",
			line: "unsupported",
			pass: true,
			want: Message{UnsupportedData{[]string{}}, "unsupported"},
		},
	}

	for _, td := range tests {
		t.Run(td.name, func(t *testing.T) {
			got, err := ParseSessionMessage(td.line)
			if td.pass != (err == nil) {
				t.Errorf("got error %v, want error %v", got, err)
			}
			if reflect.DeepEqual(got, td.want) == false {
				t.Errorf("got %v want %v", got, td.want)
			}
		})
	}
}

func TestParseControlMessage(t *testing.T) {
	tests := []struct {
		name string
		line string
		pass bool
		want Message
	}{
		{
			name: "REQOK",
			line: "REQOK,1",
			pass: true,
			want: Message{REQOKData{1}, "REQOK"},
		},
		{
			name: "REQOK (too short)",
			line: "REQOK",
			pass: false,
		},
		{
			name: "REQOK (invalid request ID)",
			line: "REQOK,a",
			pass: false,
		},
		{
			name: "REQERR",
			line: "REQERR,1,10,error",
			pass: true,
			want: Message{REQERRData{"error", 1, 10}, "REQERR"},
		},
		{
			name: "REQERR (too short)",
			line: "REQERR",
			pass: false,
		},
		{
			name: "REQERR (invalid request ID)",
			line: "REQERR,a,10,error",
			pass: false,
		},
		{
			name: "REQERR (invalid error number)",
			line: "REQERR,1,a,error",
			pass: false,
		},
	}

	for _, td := range tests {
		t.Run(td.name, func(t *testing.T) {
			got, err := ParseControlMessage(td.line)
			if td.pass != (err == nil) {
				t.Errorf("got error %v, want error %v", got, err)
			}
			if reflect.DeepEqual(got, td.want) == false {
				t.Errorf("got %v want %v", got, td.want)
			}
		})
	}
}

func TestSessionMessages(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Message
	}{
		{
			name:  "single",
			input: "CONOK,sessionID,500,5000,*\r\n",
			want:  []Message{{CONOKData{"sessionID", "*", 500, 5000}, "CONOK"}},
		},
		{
			name:  "multiple",
			input: "CONOK,sessionID,500,5000,*\r\nPROBE\r\nEND,1,ok\r\n",
			want: []Message{
				{CONOKData{"sessionID", "*", 500, 5000}, "CONOK"},
				{nil, "PROBE"},
				{ENDData{"ok", 1}, "END"},
			},
		},
		{
			name:  "stop on invalid",
			input: "CONOK,sessionID,500,5000,*\r\nSYNC,a\r\nEND,1,ok\r\n",
			want:  []Message{{CONOKData{"sessionID", "*", 500, 5000}, "CONOK"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []Message
			for msg, err := range SessionMessages(io.NopCloser(strings.NewReader(tt.input))) {
				if err == nil {
					got = append(got, msg)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v want %v", got, tt.want)
			}
		})
	}
}
