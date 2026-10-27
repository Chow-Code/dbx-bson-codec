package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func bsonFixture(t *testing.T) []byte {
	t.Helper()
	decimal, err := bson.ParseDecimal128("1234567890.123456789012345678901234")
	if err != nil {
		t.Fatal(err)
	}
	document := bson.D{
		{Key: "_id", Value: bson.ObjectID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}},
		{Key: "playerId", Value: int64(math.MaxInt64)},
		{Key: "level", Value: int32(42)},
		{Key: "name", Value: "示例玩家"},
		{Key: "created", Value: bson.DateTime(-1000)},
		{Key: "price", Value: decimal},
		{Key: "bytes", Value: bson.Binary{Subtype: 0, Data: []byte{0, 255, 128}}},
		{Key: "legacyBytes", Value: bson.Binary{Subtype: 2, Data: []byte{0, 255}}},
		{Key: "nested", Value: bson.D{{Key: "list", Value: bson.A{true, nil, int64(-7)}}}},
		{Key: "regex", Value: bson.Regex{Pattern: "^x", Options: "im"}},
		{Key: "timestamp", Value: bson.Timestamp{T: 123, I: 9}},
		{Key: "code", Value: bson.CodeWithScope{Code: "return x", Scope: bson.D{{Key: "x", Value: int32(1)}}}},
		{Key: "double", Value: math.Inf(1)},
		{Key: "undefined", Value: bson.Undefined{}},
		{Key: "symbol", Value: bson.Symbol("symbol")},
		{Key: "pointer", Value: bson.DBPointer{DB: "db.collection", Pointer: bson.ObjectID{1}}},
		{Key: "min", Value: bson.MinKey{}}, {Key: "max", Value: bson.MaxKey{}},
	}
	raw, err := bson.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBSONCanonicalTypesAndRoundTrip(t *testing.T) {
	raw := bsonFixture(t)
	decoded, err := decodeBSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"$numberLong":"9223372036854775807"`, `"$numberInt":"42"`, `"$oid":"0102030405060708090a0b0c"`, `"$numberDecimal"`, `"$binary"`, `"$timestamp"`, `"$date"`, `"$scope"`} {
		if !bytes.Contains(decoded, []byte(value)) {
			t.Errorf("missing canonical value %s: %s", value, decoded)
		}
	}
	if !json.Valid(decoded) {
		t.Fatalf("not JSON: %s", decoded)
	}
	encoded, err := encodeBSON(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, encoded) {
		t.Fatalf("BSON bytes changed after round trip\n%x\n%x", raw, encoded)
	}
}

func TestBSONRejectsInvalidInputs(t *testing.T) {
	raw := bsonFixture(t)
	badLength := append([]byte(nil), raw...)
	binary.LittleEndian.PutUint32(badLength, 5)
	badEnd := append([]byte(nil), raw...)
	badEnd[len(badEnd)-1] = 1
	duplicate, _ := bson.Marshal(bson.D{{Key: "x", Value: 1}, {Key: "x", Value: 2}})
	invalidUTF8, _ := bson.Marshal(bson.D{{Key: "x", Value: string([]byte{255})}})
	var nested any = bson.D{}
	for i := 0; i < maxBSONDepth; i++ {
		nested = bson.D{{Key: "x", Value: nested}}
	}
	deep, _ := bson.Marshal(nested)
	for name, data := range map[string][]byte{"empty": nil, "length": badLength, "terminator": badEnd, "truncated": raw[:len(raw)-1], "trailing": append(append([]byte{}, raw...), 0), "concatenated": append(append([]byte{}, raw...), raw...), "duplicate": duplicate, "utf8": invalidUTF8, "depth": deep, "unknownType": {8, 0, 0, 0, 0x42, 'x', 0, 0}, "arrayIndex": {20, 0, 0, 0, 4, 'a', 0, 12, 0, 0, 0, 16, '1', 0, 1, 0, 0, 0, 0, 0}} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeBSON(data); err == nil {
				t.Fatal("accepted malformed BSON")
			}
		})
	}
	for _, input := range []string{"", "[]", "null", `{"x":1} {"x":2}`, `{"x":{"$numberLong":"oops"}}`, strings.Repeat(`{"x":`, 101) + "0" + strings.Repeat("}", 101)} {
		if _, err := encodeBSON([]byte(input)); err == nil {
			t.Errorf("accepted invalid input %.80s", input)
		}
	}
}

func TestBSONFilterLimitsAndStreams(t *testing.T) {
	var out bytes.Buffer
	raw := bsonFixture(t)
	if err := runBSONFilter("--bson-decode", bytes.NewReader(raw), &out); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := runBSONFilter("--bson-encode", &out, &encoded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded.Bytes(), raw) {
		t.Fatal("filter changed bytes")
	}
	if err := runBSONFilter("--bson-decode", bytes.NewReader(make([]byte, maxBytes+1)), &out); err == nil {
		t.Fatal("accepted oversized input")
	}
	large, _ := bson.Marshal(bson.D{{Key: "data", Value: bson.Binary{Data: make([]byte, 1600000)}}})
	if _, err := decodeBSON(large); err == nil {
		t.Fatal("accepted oversized JSON representation")
	}
}
