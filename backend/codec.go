package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxBytes = 2 * 1024 * 1024

type Command struct {
	Program   string   `json:"program"`
	Args      []string `json:"args"`
	Directory string   `json:"directory,omitempty"`
}
type Config struct {
	Decode    Command  `json:"decode"`
	Encode    *Command `json:"encode,omitempty"`
	Format    string   `json:"format"`
	TimeoutMS int      `json:"timeoutMs"`
}
type CodecContext struct {
	DB        int    `json:"db"`
	KeyRaw    string `json:"keyRaw"`
	RedisType string `json:"redisType"`
}
type CodecRequest struct {
	CodecID   string          `json:"codecId"`
	RawBase64 string          `json:"rawBase64,omitempty"`
	Format    string          `json:"format,omitempty"`
	Value     json.RawMessage `json:"value,omitempty"`
	Context   CodecContext    `json:"context"`
}
type Decoded struct {
	Format string `json:"format"`
	Value  any    `json:"value"`
}
type plugin struct {
	configPath string
	mutex      sync.Mutex
	slots      chan struct{}
}

func parseStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("Expected one JSON value")
	}
	return nil
}
func validateCommand(command Command) error {
	if !filepath.IsAbs(command.Program) {
		return errors.New("Program must be an absolute executable path")
	}
	if command.Directory != "" && !filepath.IsAbs(command.Directory) {
		return errors.New("Working directory must be absolute")
	}
	if len(command.Args) > 128 {
		return errors.New("Too many program arguments")
	}
	if info, err := os.Stat(command.Program); err != nil || info.IsDir() {
		return errors.New("Program does not exist or is a directory")
	}
	return nil
}
func (config Config) validate() error {
	if config.TimeoutMS < 100 || config.TimeoutMS > 8000 {
		return errors.New("timeoutMs must be between 100 and 8000")
	}
	if config.Format != "json" && config.Format != "text" {
		return errors.New("format must be json or text")
	}
	if err := validateCommand(config.Decode); err != nil {
		return fmt.Errorf("Decoder: %w", err)
	}
	if config.Encode != nil {
		return validateCommand(*config.Encode)
	}
	return nil
}
func (p *plugin) loadConfig() (Config, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.readConfig()
}
func (p *plugin) readConfig() (Config, error) {
	var config Config
	data, err := os.ReadFile(p.configPath)
	if errors.Is(err, os.ErrNotExist) {
		return Config{Format: "json", TimeoutMS: 3000}, nil
	}
	if err != nil {
		return config, err
	}
	if len(data) > 64*1024 {
		return config, errors.New("Configuration exceeds 64 KiB")
	}
	err = parseStrict(data, &config)
	return config, err
}
func (p *plugin) saveConfig(config Config) error {
	if err := config.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 64*1024 {
		return errors.New("Configuration exceeds 64 KiB")
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if err := os.MkdirAll(filepath.Dir(p.configPath), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(p.configPath), ".config-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temporary, p.configPath)
}

func (p *plugin) transform(method string, request CodecRequest) (any, error) {
	config, err := p.codecConfig(request.CodecID)
	if err != nil {
		return nil, err
	}
	if err = config.validate(); err != nil {
		return nil, fmt.Errorf("Configure the external codec in its workbench first: %w", err)
	}
	if method == "redis/codec/decode" {
		if len(request.RawBase64) > base64.StdEncoding.EncodedLen(maxBytes) {
			return nil, errors.New("Input exceeds 2 MiB")
		}
		input, err := base64.StdEncoding.Strict().DecodeString(request.RawBase64)
		if err != nil || len(input) > maxBytes || base64.StdEncoding.EncodeToString(input) != request.RawBase64 {
			return nil, errors.New("Invalid or oversized base64 input")
		}
		output, err := runCommand(config.Decode, input, config.TimeoutMS, request.Context)
		if err != nil {
			return nil, err
		}
		if config.Format == "json" {
			if !utf8.Valid(output) || !json.Valid(output) {
				return nil, errors.New("Decoder output is not one valid JSON value (UTF-8 required)")
			}
			return boundedDecoded("json", json.RawMessage(output))
		}
		if !utf8.Valid(output) {
			return nil, errors.New("Text decoder output is not valid UTF-8")
		}
		return boundedDecoded("text", string(output))
	}
	if config.Encode == nil {
		return nil, errors.New("No encoder is configured")
	}
	input := []byte(request.Value)
	if request.Format == "text" {
		var value string
		if err := json.Unmarshal(request.Value, &value); err != nil {
			return nil, errors.New("Text encoding requires a string value")
		}
		input = []byte(value)
	} else if request.Format != "json" || !json.Valid(input) {
		return nil, errors.New("Encoding requires json or text format")
	}
	if len(input) > maxBytes {
		return nil, errors.New("Input exceeds 2 MiB")
	}
	output, err := runCommand(*config.Encode, input, config.TimeoutMS, request.Context)
	if err != nil {
		return nil, err
	}
	return map[string]string{"rawBase64": base64.StdEncoding.EncodeToString(output)}, nil
}

func boundedDecoded(format string, value any) (Decoded, error) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maxBytes {
		return Decoded{}, errors.New("Decoded JSON representation exceeds 2 MiB")
	}
	return Decoded{Format: format, Value: value}, nil
}

type boundedBuffer struct {
	data     bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	remaining := buffer.limit - buffer.data.Len()
	if len(data) > remaining {
		buffer.data.Write(data[:remaining])
		buffer.exceeded = true
		buffer.cancel()
		return remaining, errors.New("Program output exceeds limit")
	}
	return buffer.data.Write(data)
}

func runCommand(command Command, input []byte, timeoutMS int, metadata CodecContext) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, command.Program, command.Args...)
	cmd.Dir = command.Directory
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), fmt.Sprintf("DBX_REDIS_DB=%d", metadata.DB), "DBX_REDIS_KEY_RAW="+metadata.KeyRaw, "DBX_REDIS_TYPE="+metadata.RedisType)
	output := &boundedBuffer{limit: maxBytes, cancel: cancel}
	stderr := &boundedBuffer{limit: 16 * 1024, cancel: cancel}
	cmd.Stdout = output
	cmd.Stderr = stderr
	cmd.WaitDelay = 500 * time.Millisecond
	configureProcess(cmd)
	cmd.Cancel = func() error { return terminateProcessTree(cmd) }
	err := cmd.Run()
	if output.exceeded || stderr.exceeded {
		return nil, errors.New("Program output exceeds limit (stdout: 2 MiB, stderr: 16 KiB)")
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, errors.New("External codec timed out")
	}
	if err != nil {
		detail := strings.TrimSpace(strings.ToValidUTF8(stderr.data.String(), "?"))
		if detail != "" {
			return nil, fmt.Errorf("External codec failed: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("External codec failed: %w", err)
	}
	return output.data.Bytes(), nil
}
