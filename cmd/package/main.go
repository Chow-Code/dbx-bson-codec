// Package builds unsigned DBX candidates and verifies/extracts them for native
// smoke tests. It is a development tool and is not shipped in the plugin.
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var targets = map[string][2]string{
	"windows-x64": {"windows", "amd64"}, "darwin-arm64": {"darwin", "arm64"},
	"darwin-x64": {"darwin", "amd64"}, "linux-x64": {"linux", "amd64"}, "linux-arm64": {"linux", "arm64"},
}

type checksums struct {
	Algorithm string            `json:"algorithm"`
	Files     map[string]string `json:"files"`
}
type artifact struct {
	Target string `json:"target"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
}

func hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func jsonBytes(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	return append(data, '\n'), err
}

func main() {
	target := flag.String("target", "windows-x64", "DBX target")
	verify := flag.String("verify", "", "unsigned package to verify")
	extract := flag.String("extract", "", "new directory for verified extraction")
	flag.Parse()
	var err error
	if *verify != "" {
		err = verifyPackage(*verify, *extract)
	} else {
		err = buildPackage(*target)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func buildPackage(target string) error {
	platform, ok := targets[target]
	if !ok {
		return fmt.Errorf("unsupported target %q", target)
	}
	manifestBytes, err := os.ReadFile("manifest.json")
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err = json.Unmarshal(manifestBytes, &manifest); err != nil {
		return err
	}
	version, ok := manifest["version"].(string)
	if !ok {
		return errors.New("missing version")
	}
	id, ok := manifest["id"].(string)
	if !ok {
		return errors.New("missing id")
	}
	binaryPath := "bin/" + target + "/backend"
	if platform[0] == "windows" {
		binaryPath += ".exe"
	}
	entries, ok := manifest["entrypoints"].(map[string]any)
	if !ok {
		return errors.New("missing entrypoints")
	}
	backend, ok := entries["backend"].(map[string]any)
	if !ok {
		return errors.New("missing backend entrypoint")
	}
	backend["executable"] = binaryPath
	files := map[string][]byte{}
	files["manifest.json"], err = jsonBytes(manifest)
	if err != nil {
		return err
	}
	for _, name := range []string{"assets", "ui", "THIRD_PARTY_LICENSES", "LICENSE", "NOTICE"} {
		err = filepath.WalkDir(name, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("not a regular file: %s", path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(path)] = data
			return nil
		})
		if err != nil {
			return err
		}
	}
	if err = os.MkdirAll("work", 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("work", "build-"+target+"-")
	if err != nil {
		return err
	}
	executable := filepath.Join(stage, "backend")
	if platform[0] == "windows" {
		executable += ".exe"
	}
	command := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-ldflags=-s -w", "-o", executable, "./backend")
	// Replace target variables rather than appending duplicate environment keys.
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if key != "GOOS" && key != "GOARCH" && key != "CGO_ENABLED" {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "GOOS="+platform[0], "GOARCH="+platform[1], "CGO_ENABLED=0")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err = command.Run(); err != nil {
		return err
	}
	files[binaryPath], err = os.ReadFile(executable)
	if err != nil {
		return err
	}
	checks := checksums{Algorithm: "sha256", Files: map[string]string{}}
	for name, data := range files {
		checks.Files[name] = hash(data)
	}
	files["checksums.json"], err = jsonBytes(checks)
	if err != nil {
		return err
	}
	directory := filepath.Join("dist", version)
	if err = os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	filename := id + "-" + version + "-" + target + ".dbxp"
	destination := filepath.Join(directory, filename)
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		header.SetMode(0644)
		if name == binaryPath {
			header.SetMode(0755)
		}
		entry, writeErr := writer.CreateHeader(header)
		if writeErr == nil {
			_, writeErr = entry.Write(files[name])
		}
		if writeErr != nil {
			writer.Close()
			file.Close()
			return writeErr
		}
	}
	if err = writer.Close(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		return err
	}
	metadata := artifact{Target: target, URL: "https://github.com/Chow-Code/dbx-bson-codec/releases/download/v" + version + "/" + filename, SHA256: hash(data), Size: len(data)}
	encoded, err := jsonBytes(metadata)
	if err != nil {
		return err
	}
	if err = os.WriteFile(destination+".artifact.json", encoded, 0644); err != nil {
		return err
	}
	if err = os.WriteFile(destination+".sha256", []byte(metadata.SHA256+"  "+filename+"\n"), 0644); err != nil {
		return err
	}
	fmt.Println(destination)
	return nil
}

func verifyPackage(filename, destination string) error {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}
	defer archive.Close()
	files := map[string][]byte{}
	for _, entry := range archive.File {
		name := entry.Name
		if !fs.ValidPath(name) || strings.Contains(name, "\\") || strings.Contains(name, ":") || entry.Mode()&os.ModeType != 0 {
			return fmt.Errorf("unsafe package entry %q", name)
		}
		if _, exists := files[name]; exists {
			return errors.New("duplicate package entry")
		}
		if entry.UncompressedSize64 > 32*1024*1024 {
			return errors.New("oversized package entry")
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, 32*1024*1024+1))
		reader.Close()
		if readErr != nil {
			return readErr
		}
		if len(data) > 32*1024*1024 {
			return errors.New("oversized package entry")
		}
		files[name] = data
	}
	if _, exists := files["signature.json"]; exists {
		return errors.New("expected unsigned candidate")
	}
	var checks checksums
	if err = json.Unmarshal(files["checksums.json"], &checks); err != nil {
		return err
	}
	if checks.Algorithm != "sha256" || len(checks.Files)+1 != len(files) {
		return errors.New("invalid checksum coverage")
	}
	for name, expected := range checks.Files {
		data, exists := files[name]
		if !exists || hash(data) != expected {
			return fmt.Errorf("checksum mismatch: %s", name)
		}
	}
	var manifest struct {
		Entrypoints struct {
			Backend struct {
				Executable string `json:"executable"`
			} `json:"backend"`
		} `json:"entrypoints"`
	}
	if err = json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		return err
	}
	executable := manifest.Entrypoints.Backend.Executable
	if _, exists := files[executable]; !exists {
		return errors.New("missing backend executable")
	}
	if destination == "" {
		return errors.New("-extract is required")
	}
	if _, err = os.Stat(destination); !os.IsNotExist(err) {
		return errors.New("extraction directory must not exist")
	}
	if err = os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	for name, data := range files {
		path := filepath.Join(destination, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err = os.WriteFile(path, data, 0644); err != nil {
			return err
		}
		if name == executable {
			if err = os.Chmod(path, 0755); err != nil {
				return err
			}
		}
	}
	fmt.Printf("PASS: %d unsigned package entries verified; extracted backend: %s\n", len(files), executable)
	return nil
}
