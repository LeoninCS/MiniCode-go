package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/cli"
)

func TestInputNonInteractive(t *testing.T) {
	var stdout bytes.Buffer
	input, err := cli.NewInput(strings.NewReader("first\nsecond\n"), &stdout, io.Discard, false)
	if err != nil {
		t.Fatalf("NewInput: %v", err)
	}
	defer func() { _ = input.Close() }()

	line, err := input.Readline("> ")
	if err != nil || line != "first" {
		t.Fatalf("first Readline = %q, %v", line, err)
	}
	line, err = input.Readline("> ")
	if err != nil || line != "second" {
		t.Fatalf("second Readline = %q, %v", line, err)
	}
	line, err = input.Readline("> ")
	if err != io.EOF || line != "" {
		t.Fatalf("EOF Readline = %q, %v", line, err)
	}
	if got, want := stdout.String(), "> > > \n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

type stringReadCloser struct {
	*strings.Reader
}

func (stringReadCloser) Close() error { return nil }

func TestInputShiftEnter(t *testing.T) {
	for name, sequence := range map[string]string{
		"control-j":               "\n",
		"kitty CSI-u":             "\x1b[13;2u",
		"xterm modifyOtherKeys":   "\x1b[27;2;13~",
		"modified function key":   "\x1b[13;2~",
		"kitty encoded control-j": "\x1b[106;5u",
	} {
		t.Run(name, func(t *testing.T) {
			stdin := stringReadCloser{strings.NewReader("first" + sequence + "second\r")}
			var stdout bytes.Buffer
			input, err := cli.NewInput(stdin, &stdout, io.Discard, true)
			if err != nil {
				t.Fatalf("NewInput: %v", err)
			}
			defer func() { _ = input.Close() }()

			line, err := input.Readline("> ")
			if err != nil || line != "first\nsecond" {
				t.Fatalf("Readline = %q, %v", line, err)
			}
			if !strings.HasPrefix(stdout.String(), "\x1b[>1u") {
				t.Fatalf("extended keyboard protocol was not enabled: %q", stdout.String())
			}
		})
	}
}

func TestInputInteractiveEditing(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys string
		want string
	}{
		{name: "Chinese cursor movement", keys: "中文\x1b[D好\r", want: "中好文"},
		{name: "emoji cursor movement", keys: "你🙂好\x1b[D\x1b[D真\r", want: "你真🙂好"},
		{name: "home end and delete", keys: "abc\x1b[H\x1b[3~\x1b[Fd\r", want: "bcd"},
		{name: "backspace", keys: "中文\x7f好\r", want: "中好"},
		{name: "newline at cursor", keys: "ab\x1b[D\x1b[13;2uc\r", want: "a\ncb"},
		{name: "up within multiline input", keys: "abcd\nxy\x1b[D\x1b[AX\r", want: "aXbcd\nxy"},
		{name: "down within multiline input", keys: "abcd\nxyz\x1b[A\x1b[D\x1b[BX\r", want: "abcd\nxyXz"},
		{name: "column preserved through short line", keys: "abcdef\nx\nuvwxyz\x1b[A\x1b[AX\r", want: "abcdefX\nx\nuvwxyz"},
		{name: "column preserved through empty line", keys: "abc\n\nxyz\x1b[A\x1b[AX\r", want: "abcX\n\nxyz"},
		{name: "multiline home", keys: "ab\ncd\x1b[HX\r", want: "ab\nXcd"},
		{name: "multiline end", keys: "abcd\nx\x1b[A\x1b[FX\r", want: "abcdX\nx"},
		{name: "backspace merges lines", keys: "ab\ncd\x1b[H\x7f\r", want: "abcd"},
		{name: "delete merges lines", keys: "ab\ncd\x1b[A\x1b[3~\r", want: "abcd"},
		{name: "vertical movement uses display columns", keys: "中文abc\n12345\x1b[AX\r", want: "中文aXbc\n12345"},
		{name: "vertical movement around wide characters", keys: "中文\nabc\x1b[AX\r", want: "中X文\nabc"},
		{name: "literal newline marker is preserved", keys: "a↵b\ue000\r", want: "a↵b\ue000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, err := cli.NewInput(stringReadCloser{strings.NewReader(tc.keys)}, io.Discard, io.Discard, true)
			if err != nil {
				t.Fatalf("NewInput: %v", err)
			}
			defer func() { _ = input.Close() }()

			line, err := input.Readline("> ")
			if err != nil || line != tc.want {
				t.Fatalf("Readline = %q, %v; want %q", line, err, tc.want)
			}
		})
	}
}

func TestInputInteractiveVerticalMovementDoesNotRecallHistory(t *testing.T) {
	stdin := stringReadCloser{strings.NewReader("old\r\x1b[A\x1b[B\r" +
		"draft\ntext\x1b[A\x1b[AX\x1b[B\x1b[BY\r")}
	input, err := cli.NewInput(stdin, io.Discard, io.Discard, true)
	if err != nil {
		t.Fatalf("NewInput: %v", err)
	}
	defer func() { _ = input.Close() }()

	for _, want := range []string{"old", "", "drafXt\ntextY"} {
		line, err := input.Readline("> ")
		if err != nil || line != want {
			t.Fatalf("Readline = %q, %v; want %q", line, err, want)
		}
	}
}

func TestInputInteractiveRequiresClosableStdin(t *testing.T) {
	_, err := cli.NewInput(strings.NewReader(""), io.Discard, io.Discard, true)
	if err == nil || !strings.Contains(err.Error(), "closable stdin") {
		t.Fatalf("error = %v", err)
	}
}

func TestInputInterrupt(t *testing.T) {
	for name, sequence := range map[string]string{
		"legacy": "\x03",
		"kitty":  "\x1b[99;5u",
		"xterm":  "\x1b[27;5;99~",
	} {
		t.Run(name, func(t *testing.T) {
			input, err := cli.NewInput(stringReadCloser{strings.NewReader("draft\ntext" + sequence)}, io.Discard, io.Discard, true)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = input.Close() }()
			line, err := input.Readline("> ")
			if !errors.Is(err, context.Canceled) || line != "" {
				t.Fatalf("Readline = %q, %v; want context cancellation without submitting input", line, err)
			}
		})
	}
}

func TestInputKeyboardProtocolOnlyWhileReading(t *testing.T) {
	var stdout bytes.Buffer
	input, err := cli.NewInput(stringReadCloser{strings.NewReader("first\rsecond\r")}, &stdout, io.Discard, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	if stdout.Len() != 0 {
		t.Fatalf("keyboard protocol enabled before reading input: %q", stdout.String())
	}
	for _, want := range []string{"first", "second"} {
		stdout.Reset()
		line, err := input.Readline("> ")
		if err != nil || line != want {
			t.Fatalf("Readline = %q, %v; want %q", line, err, want)
		}
		if strings.Count(stdout.String(), "\x1b[>1u") != 1 || strings.Count(stdout.String(), "\x1b[<u") != 1 {
			t.Fatalf("keyboard protocol not restored after input: %q", stdout.String())
		}
	}
	stdout.Reset()
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "\x1b[<u") {
		t.Fatalf("Close restored keyboard protocol a second time: %q", stdout.String())
	}
}
