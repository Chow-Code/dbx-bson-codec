package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/x/bsonx/bsoncore"
)

const maxBSONDepth = 100

func (p *plugin) codecConfig(codecID string) (Config, error) {
	switch codecID {
	case "external-program":
		return p.loadConfig()
	case "bson":
		// Run the bundled converter as a bounded external process, just like
		// user-defined converters. No executable path needs to be configured.
		executable, err := os.Executable()
		if err != nil {
			return Config{}, err
		}
		encoder := Command{Program: executable, Args: []string{"--bson-encode"}}
		return Config{Decode: Command{Program: executable, Args: []string{"--bson-decode"}}, Encode: &encoder, Format: "json", TimeoutMS: 3000}, nil
	default:
		return Config{}, errors.New("Unknown codec")
	}
}

func runBSONFilter(mode string, input io.Reader, output io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(input, maxBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxBytes {
		return errors.New("Input exceeds 2 MiB")
	}
	var result []byte
	switch mode {
	case "--bson-decode":
		result, err = decodeBSON(data)
	case "--bson-encode":
		result, err = encodeBSON(data)
	default:
		return errors.New("Unknown BSON filter mode")
	}
	if err != nil {
		return err
	}
	if len(result) > maxBytes {
		return errors.New("Output exceeds 2 MiB")
	}
	_, err = output.Write(result)
	return err
}

func decodeBSON(data []byte) ([]byte, error) {
	if len(data) > maxBytes {
		return nil, errors.New("BSON input exceeds 2 MiB")
	}
	if err := checkBSONDocument(data, 0, false); err != nil {
		return nil, err
	}
	raw := bson.Raw(data)
	if err := raw.Validate(); err != nil {
		return nil, fmt.Errorf("Invalid BSON: %w", err)
	}
	result, err := bson.MarshalExtJSON(raw, true, true)
	if err != nil {
		return nil, fmt.Errorf("BSON conversion failed: %w", err)
	}
	if len(result) > maxBytes {
		return nil, errors.New("Decoded JSON representation exceeds 2 MiB")
	}
	return result, nil
}

func encodeBSON(data []byte) ([]byte, error) {
	if len(data) > maxBytes {
		return nil, errors.New("JSON input exceeds 2 MiB")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' || !utf8.Valid(data) || !json.Valid(data) {
		return nil, errors.New("BSON encoding requires one UTF-8 Extended JSON document")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if delimiter, ok := token.(json.Delim); ok {
			if delimiter == '{' || delimiter == '[' {
				depth++
			} else {
				depth--
			}
			if depth > maxBSONDepth {
				return nil, errors.New("JSON nesting exceeds 100 levels")
			}
		}
	}
	var document bson.D
	if err := bson.UnmarshalExtJSON(data, false, &document); err != nil {
		return nil, fmt.Errorf("Invalid Extended JSON: %w", err)
	}
	result, err := bson.Marshal(document)
	if err != nil {
		return nil, err
	}
	if len(result) > maxBytes {
		return nil, errors.New("Encoded BSON exceeds 2 MiB")
	}
	if err := checkBSONDocument(result, 0, false); err != nil {
		return nil, err
	}
	return result, nil
}

// Bound nesting before the driver's recursive validation. Walk element slices
// without materializing whole documents; reject duplicate fields rather than
// silently losing them when the host renders an ordinary JSON object.
func checkBSONDocument(data []byte, depth int, array bool) error {
	if depth >= maxBSONDepth {
		return errors.New("BSON nesting exceeds 100 levels")
	}
	if len(data) < 5 || int(int32(binary.LittleEndian.Uint32(data))) != len(data) || data[len(data)-1] != 0 {
		return errors.New("Invalid BSON document length or terminator (exactly one document required)")
	}
	remaining := data[4 : len(data)-1]
	keys := make(map[string]struct{})
	index := 0
	for len(remaining) != 0 {
		element, rest, ok := bsoncore.ReadElement(remaining)
		if !ok || len(rest) >= len(remaining) {
			return errors.New("Invalid BSON element")
		}
		remaining = rest
		key, err := element.KeyErr()
		if err != nil || !utf8.ValidString(key) {
			return errors.New("Invalid BSON field name")
		}
		if _, duplicate := keys[key]; duplicate {
			return errors.New("Duplicate BSON field names cannot be represented safely in the JSON viewer")
		}
		keys[key] = struct{}{}
		if array && key != strconv.Itoa(index) {
			return errors.New("Invalid BSON array index")
		}
		index++
		value, err := element.ValueErr()
		if err != nil {
			return err
		}
		switch value.Type {
		case bsoncore.TypeEmbeddedDocument, bsoncore.TypeArray:
			if err := checkBSONDocument(value.Data, depth+1, value.Type == bsoncore.TypeArray); err != nil {
				return err
			}
		case bsoncore.TypeCodeWithScope:
			code, scope, trailing, ok := bsoncore.ReadCodeWithScope(value.Data)
			if !ok || len(trailing) != 0 || !utf8.ValidString(code) {
				return errors.New("Invalid BSON code with scope")
			}
			if err := checkBSONDocument(scope, depth+1, false); err != nil {
				return err
			}
		case bsoncore.TypeString, bsoncore.TypeJavaScript, bsoncore.TypeSymbol:
			text, trailing, ok := bsoncore.ReadString(value.Data)
			if !ok || len(trailing) != 0 || !utf8.ValidString(text) {
				return errors.New("Invalid BSON UTF-8 string")
			}
		case bsoncore.TypeRegex:
			pattern, options, trailing, ok := bsoncore.ReadRegex(value.Data)
			if !ok || len(trailing) != 0 || !utf8.ValidString(pattern) || !utf8.ValidString(options) {
				return errors.New("Invalid BSON regex")
			}
		}
	}
	return nil
}
