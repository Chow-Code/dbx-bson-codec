package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCodecProcess(t *testing.T) {
	for index, arg := range os.Args {
		if arg != "--codec-helper" {
			continue
		}
		mode := os.Args[index+1]
		input, _ := io.ReadAll(os.Stdin)
		switch mode {
		case "xor":
			for i := range input {
				input[i] ^= 0xa5
			}
			os.Stdout.Write(input)
		case "echo":
			os.Stdout.Write(input)
		case "sleep":
			time.Sleep(20 * time.Second)
		case "spawn":
			executable, _ := os.Executable()
			child := exec.Command(executable, "-test.run=TestCodecProcess", "--", "--codec-helper", "sleep")
			configureProcess(child)
			child.Stdout = os.Stdout
			child.Stderr = os.Stderr
			if err := child.Start(); err != nil {
				os.Exit(2)
			}
			if err := os.WriteFile(os.Args[index+2], []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
				os.Exit(2)
			}
			time.Sleep(20 * time.Second)
		case "fail":
			fmt.Fprintln(os.Stderr, "fixture failure")
			os.Exit(3)
		case "flood":
			os.Stdout.Write(bytes.Repeat([]byte("x"), maxBytes+1024))
		case "stderr":
			os.Stderr.Write(bytes.Repeat([]byte("x"), 32*1024))
		case "invalid":
			os.Stdout.Write([]byte{0xff})
		case "invalidjson":
			os.Stdout.Write([]byte{'"', 0xff, '"'})
		}
		os.Exit(0)
	}
}

func helper(t *testing.T, mode string) Command {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Command{Program: executable, Args: []string{"-test.run=TestCodecProcess", "--", "--codec-helper", mode}}
}
func fixture(t *testing.T, command Command, format string) *plugin {
	t.Helper()
	p := &plugin{configPath: filepath.Join(t.TempDir(), "config.json"), slots: make(chan struct{}, 4)}
	if err := p.saveConfig(Config{Decode: command, Encode: &command, Format: format, TimeoutMS: 2000}); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBinaryRoundTripAndPersistence(t *testing.T) {
	p := fixture(t, helper(t, "xor"), "json")
	value := []byte(`{"name":"Ada","level":42}`)
	raw := append([]byte(nil), value...)
	for i := range raw {
		raw[i] ^= 0xa5
	}
	request := CodecRequest{CodecID: "external-program", RawBase64: base64.StdEncoding.EncodeToString(raw)}
	result, err := p.transform("redis/codec/decode", request)
	if err != nil {
		t.Fatal(err)
	}
	decoded := result.(Decoded)
	encodedValue, _ := json.Marshal(decoded.Value)
	result, err = p.transform("redis/codec/encode", CodecRequest{CodecID: "external-program", Format: "json", Value: encodedValue})
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]string)["rawBase64"] != request.RawBase64 {
		t.Fatal("binary round trip changed bytes")
	}
	config, err := p.loadConfig()
	if err != nil || config.Decode.Program == "" {
		t.Fatalf("configuration was not retained: %+v %v", config, err)
	}
	config.Format = "text"
	if err = p.saveConfig(config); err != nil {
		t.Fatal(err)
	}
	loaded, _ := p.loadConfig()
	if loaded.Format != "text" {
		t.Fatal("configuration replacement was not retained")
	}
}

func TestExternalProgramFailures(t *testing.T) {
	for _, test := range []struct{ mode, expected string }{{"sleep", "timed out"}, {"fail", "fixture failure"}, {"flood", "output exceeds limit"}, {"stderr", "output exceeds limit"}, {"invalid", "not valid UTF-8"}} {
		t.Run(test.mode, func(t *testing.T) {
			p := fixture(t, helper(t, test.mode), "text")
			config, _ := p.loadConfig()
			if test.mode == "sleep" {
				config.TimeoutMS = 200
			}
			p.saveConfig(config)
			started := time.Now()
			_, err := p.transform("redis/codec/decode", CodecRequest{CodecID: "external-program", RawBase64: "AA=="})
			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("expected %q, got %v", test.expected, err)
			}
			if time.Since(started) > 3*time.Second {
				t.Fatal("program did not stop promptly")
			}
		})
	}
}

func TestValidationAndTextEncoding(t *testing.T) {
	p := fixture(t, helper(t, "echo"), "text")
	for _, raw := range []string{"!", "YR==", "YQ==\n", strings.Repeat("A", 3*1024*1024)} {
		if _, err := p.transform("redis/codec/decode", CodecRequest{CodecID: "external-program", RawBase64: raw}); err == nil {
			t.Fatal("accepted invalid base64")
		}
	}
	result, err := p.transform("redis/codec/encode", CodecRequest{CodecID: "external-program", Format: "text", Value: json.RawMessage(`"你好\u0000world"`)})
	if err != nil {
		t.Fatal(err)
	}
	output, _ := base64.StdEncoding.DecodeString(result.(map[string]string)["rawBase64"])
	if string(output) != "你好\x00world" {
		t.Fatalf("text encoding changed bytes: %q", output)
	}
	if _, err := p.transform("redis/codec/encode", CodecRequest{CodecID: "external-program", Format: "text", Value: json.RawMessage(`1`)}); err == nil {
		t.Fatal("accepted non-string text")
	}
	config, _ := p.loadConfig()
	config.Format = "json"
	p.saveConfig(config)
	if _, err := p.transform("redis/codec/decode", CodecRequest{CodecID: "external-program", RawBase64: "eA=="}); err == nil {
		t.Fatal("accepted invalid JSON")
	}
	invalidUTF8 := fixture(t, helper(t, "invalidjson"), "json")
	if _, err := invalidUTF8.transform("redis/codec/decode", CodecRequest{CodecID: "external-program"}); err == nil {
		t.Fatal("accepted non-UTF-8 JSON")
	}
	config.Decode.Program = "relative.exe"
	if p.saveConfig(config) == nil {
		t.Fatal("accepted relative command")
	}
	if _, err := boundedDecoded("text", strings.Repeat("\x00", 400000)); err == nil {
		t.Fatal("accepted oversized JSON representation")
	}
}
