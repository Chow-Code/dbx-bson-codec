package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVerifyPackage(t *testing.T) {
	for _, kind := range []string{"valid", "tampered", "uncovered", "traversal", "duplicate", "signed"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			files := map[string][]byte{
				"manifest.json": []byte(`{"entrypoints":{"backend":{"executable":"bin/backend"}}}`),
				"bin/backend":   []byte("fixture backend"),
			}
			sums := checksums{Algorithm: "sha256", Files: map[string]string{}}
			for name, data := range files {
				sums.Files[name] = hash(data)
			}
			encoded, err := jsonBytes(sums)
			if err != nil {
				t.Fatal(err)
			}
			files["checksums.json"] = encoded
			switch kind {
			case "tampered":
				files["bin/backend"] = []byte("changed")
			case "uncovered":
				files["extra.txt"] = []byte("uncovered")
			case "traversal":
				files["../escape"] = []byte("escape")
			case "signed":
				files["signature.json"] = []byte("{}")
			}
			filename := filepath.Join(root, "candidate.dbxp")
			file, err := os.Create(filename)
			if err != nil {
				t.Fatal(err)
			}
			writer := zip.NewWriter(file)
			for name, data := range files {
				entry, err := writer.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "duplicate" {
				if _, err := writer.Create("manifest.json"); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(root, "extracted")
			err = verifyPackage(filename, destination)
			if kind != "valid" {
				if err == nil {
					t.Fatal("invalid archive accepted")
				}
				if _, err := os.Stat(destination); !os.IsNotExist(err) {
					t.Fatal("invalid archive was extracted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(filepath.Join(destination, "bin", "backend"))
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0755 {
				t.Fatalf("backend mode = %v", info.Mode())
			}
			if err := verifyPackage(filename, destination); err == nil {
				t.Fatal("existing extraction directory accepted")
			}
		})
	}
}
