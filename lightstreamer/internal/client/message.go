package client

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
)

var _ slog.LogValuer = &Message{}

type Message struct {
	Data        any
	MessageType MessageType
}

func (m Message) LogValue() slog.Value {
	attrs := make([]slog.Attr, 1, 2)
	attrs[0] = slog.String("type", string(m.MessageType))
	if m.MessageType != "PROBE" {
		attrs = append(attrs, slog.Any("data", m.Data))
	}
	return slog.GroupValue(attrs...)
}

type MessageType string

type CONOKData struct {
	SessionID     string
	ControlLink   string
	RequestLimit  int
	KeepAliveTime int
}

type SERVNAMEData struct {
	ServerName string
}

type CLIENTIPData struct {
	ClientIP string
}

type NOOPData struct {
	Preamble []string
}

type CONSData struct {
	Bandwidth float64
}

type SYNCData struct {
	SecondsSinceInitialHeader int
}

type LOOPData struct {
	ExpectedDelay int
}

type ENDData struct {
	Message string
	Code    int
}

type UData struct {
	Values         []string
	SubscriptionID int
	Item           int
}

type SUBOKData struct {
	SubscriptionID int
	Items          int
	Fields         int
}

type CONFData struct {
	SubscriptionID int
	MaxFrequency   float64
	Filtered       bool
}

type PROGData struct {
	Progressive int
}

type UnsupportedData struct {
	Values []string
}

var (
	sessionMessageParsers = map[string]func([]string) (any, error){
		"CONOK":    parseCONOK,
		"SERVNAME": parseSERVNAME,
		"CLIENTIP": parseCLIENTIP,
		"NOOP":     parseNOOP,
		"CONS":     parseCONS,
		"SYNC":     parseSYNC,
		"PROBE":    parsePROBE,
		"LOOP":     parseLOOP,
		"END":      parseEND,
		"U":        parseU,
		"SUBOK":    parseSUBOK,
		"CONF":     parseCONF,
		"PROG":     parsePROG,
	}

	controlMessageParsers = map[string]func([]string) (any, error){
		"REQOK":  parseREQOK,
		"REQERR": parseREQERR,
	}
)

func ParseSessionMessage(line string) (Message, error) {
	return parseMessage(line, sessionMessageParsers)
}

func ParseControlMessage(line string) (Message, error) {
	return parseMessage(line, controlMessageParsers)
}

func parseMessage(line string, parsers map[string]func([]string) (any, error)) (Message, error) {
	parts := strings.Split(line, ",")
	var data any
	var err error
	if f, ok := parsers[parts[0]]; ok {
		data, err = f(parts[1:])
	} else {
		data = UnsupportedData{Values: parts[1:]}
	}
	if err != nil {
		return Message{}, fmt.Errorf("parse: %w", err)
	}
	return Message{MessageType: MessageType(parts[0]), Data: data}, nil
}

func parseCONOK(parts []string) (any, error) {
	if len(parts) != 4 {
		return nil, fmt.Errorf("expected 4 arguments, got %d", len(parts))
	}
	data := CONOKData{
		SessionID:   parts[0],
		ControlLink: parts[3],
	}
	var err error
	if data.RequestLimit, err = strconv.Atoi(parts[1]); err != nil {
		return nil, fmt.Errorf("invalid request limit %q: %w", parts[1], err)
	}
	if data.KeepAliveTime, err = strconv.Atoi(parts[2]); err != nil {
		return nil, fmt.Errorf("invalid keep alive time %q: %w", parts[2], err)
	}
	return data, nil
}

func parseSERVNAME(parts []string) (any, error) {
	if len(parts) != 1 {
		return nil, fmt.Errorf("expected 1 argument, got %d", len(parts))
	}
	return SERVNAMEData{ServerName: parts[0]}, nil
}

func parseCLIENTIP(parts []string) (any, error) {
	if len(parts) != 1 {
		return nil, fmt.Errorf("expected 1 argument, got %d", len(parts))
	}
	return CLIENTIPData{ClientIP: parts[0]}, nil
}

func parseNOOP(parts []string) (any, error) {
	return NOOPData{Preamble: parts}, nil
}

func parseCONS(parts []string) (any, error) {
	if len(parts) != 1 {
		return nil, fmt.Errorf("expected 1 argument, got %d", len(parts))
	}
	var data CONSData
	var err error
	if data.Bandwidth, err = parseFloatWithUnlimited(parts[0]); err != nil {
		return nil, fmt.Errorf("invalid bandwidth %q: %w", parts[0], err)
	}
	return data, nil
}

func parseSYNC(parts []string) (any, error) {
	if len(parts) != 1 {
		return nil, fmt.Errorf("expected 1 argument, got %d", len(parts))
	}
	var data SYNCData
	var err error
	if data.SecondsSinceInitialHeader, err = strconv.Atoi(parts[0]); err != nil {
		return nil, fmt.Errorf("invalid second since initial header %q: %w", parts[0], err)
	}
	return data, nil
}

func parsePROBE(_ []string) (any, error) {
	return nil, nil
}

func parseLOOP(parts []string) (any, error) {
	if len(parts) != 1 {
		return nil, fmt.Errorf("expected 1 argument, got %d", len(parts))
	}
	var data LOOPData
	var err error
	if data.ExpectedDelay, err = strconv.Atoi(parts[0]); err != nil {
		return nil, fmt.Errorf("invalid expected delay %q: %w", parts[0], err)
	}
	return data, nil
}

func parseEND(parts []string) (any, error) {
	if len(parts) != 2 {
		return nil, fmt.Errorf("expected 2 arguments, got %d", len(parts))
	}
	data := ENDData{Message: parts[1]}
	var err error
	if data.Code, err = strconv.Atoi(parts[0]); err != nil {
		return nil, fmt.Errorf("invalid code %q: %w", parts[0], err)
	}
	return data, nil
}

func parseU(parts []string) (any, error) {
	if len(parts) != 3 {
		return nil, fmt.Errorf("expected 3 arguments, got %d", len(parts))
	}
	var data UData
	var err error
	if data.SubscriptionID, err = strconv.Atoi(parts[0]); err != nil {
		return nil, fmt.Errorf("invalid subscription ID %q: %w", parts[0], err)
	}
	if data.Item, err = strconv.Atoi(parts[1]); err != nil {
		return nil, fmt.Errorf("invalid item %q: %w", parts[1], err)
	}
	data.Values = strings.Split(parts[2], "|")
	return data, nil
}

func parseSUBOK(parts []string) (any, error) {
	if len(parts) != 3 {
		return nil, fmt.Errorf("expected 3 arguments, got %d", len(parts))
	}
	var data SUBOKData
	var err error
	if data.SubscriptionID, err = strconv.Atoi(parts[0]); err != nil {
		return nil, fmt.Errorf("invalid subscription ID %q: %w", parts[0], err)
	}
	if data.Items, err = strconv.Atoi(parts[1]); err != nil {
		return nil, fmt.Errorf("invalid item %q: %w", parts[1], err)
	}
	if data.Fields, err = strconv.Atoi(parts[2]); err != nil {
		return nil, fmt.Errorf("invalid field count %q: %w", parts[2], err)
	}
	return data, nil
}

func parseCONF(parts []string) (any, error) {
	if len(parts) != 3 {
		return nil, fmt.Errorf("expected 3 arguments, got %d", len(parts))
	}
	var data CONFData
	var err error
	if data.SubscriptionID, err = strconv.Atoi(parts[0]); err != nil {
		return nil, fmt.Errorf("invalid subscription ID %q: %w", parts[0], err)
	}
	if data.MaxFrequency, err = parseFloatWithUnlimited(parts[1]); err != nil {
		return nil, fmt.Errorf("invalid max frequency %q: %w", parts[1], err)
	}
	switch parts[2] {
	case "filtered":
		data.Filtered = true
	case "unfiltered":
		data.Filtered = false
	default:
		return nil, fmt.Errorf("invalid filtered option %q", parts[2])
	}
	return data, nil
}

func parsePROG(parts []string) (any, error) {
	if len(parts) != 1 {
		return nil, fmt.Errorf("expected 1 argument, got %d", len(parts))
	}
	var data PROGData
	var err error
	if data.Progressive, err = strconv.Atoi(parts[0]); err != nil {
		return nil, fmt.Errorf("invalid progress %q: %w", parts[0], err)
	}
	return data, nil
}

func parseFloatWithUnlimited(value string) (float64, error) {
	if value == "unlimited" {
		return math.Inf(1), nil
	}
	return strconv.ParseFloat(value, 64)
}

type REQOKData struct {
	RequestID int
}

type REQERRData struct {
	ErrorMessage string
	RequestID    int
	ErrorCode    int
}

func parseREQOK(parts []string) (any, error) {
	if len(parts) != 1 {
		return nil, fmt.Errorf("expected 1 argument, got %d", len(parts))
	}
	var data REQOKData
	var err error
	if data.RequestID, err = strconv.Atoi(parts[0]); err != nil {
		return nil, fmt.Errorf("invalid request ID %q: %w", parts[0], err)
	}
	return data, nil
}

func parseREQERR(parts []string) (any, error) {
	if len(parts) != 3 {
		return nil, fmt.Errorf("expected 3 argument, got %d", len(parts))
	}
	data := REQERRData{ErrorMessage: parts[2]}
	var err error
	if data.RequestID, err = strconv.Atoi(parts[0]); err != nil {
		return nil, fmt.Errorf("invalid request ID %q: %w", parts[0], err)
	}
	if data.ErrorCode, err = strconv.Atoi(parts[1]); err != nil {
		return nil, fmt.Errorf("invalid error code %q: %w", parts[1], err)
	}
	return data, nil
}
