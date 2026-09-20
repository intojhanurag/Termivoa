package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
)

func TestCanonicalSchemaListsEveryMessageExactlyOnce(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate schema test")
	}
	schemaPath := filepath.Join(filepath.Dir(source), "..", "..", "protocol", "v1", "envelope.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Schema string `json:"$schema"`
		OneOf  []any  `json:"oneOf"`
		Defs   struct {
			MessageType struct {
				Enum []string `json:"enum"`
			} `json:"messageType"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Schema != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("schema draft = %q", schema.Schema)
	}
	if len(schema.OneOf) != len(schema.Defs.MessageType.Enum) {
		t.Fatalf("message branches = %d, enum values = %d", len(schema.OneOf), len(schema.Defs.MessageType.Enum))
	}

	want := make([]string, 0, len(clientMessageTypes)+len(hostMessageTypes))
	for messageType := range clientMessageTypes {
		want = append(want, string(messageType))
		if hostMessageTypes[messageType] {
			t.Fatalf("message %q is both client and host direction", messageType)
		}
	}
	for messageType := range hostMessageTypes {
		want = append(want, string(messageType))
	}
	sort.Strings(want)
	got := append([]string(nil), schema.Defs.MessageType.Enum...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema message types = %q, validators = %q", got, want)
	}
}

func TestGenerationIndexReferencesCanonicalEnvelopeSchema(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate schema test")
	}
	indexPath := filepath.Join(filepath.Dir(source), "..", "..", "protocol", "v1", "types.schema.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	var index struct {
		Properties map[string]struct {
			Ref string `json:"$ref"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	if len(index.Properties) == 0 {
		t.Fatal("generation index has no properties")
	}
	for name, property := range index.Properties {
		if len(property.Ref) < len("envelope.schema.json#/$defs/") || property.Ref[:len("envelope.schema.json#/$defs/")] != "envelope.schema.json#/$defs/" {
			t.Fatalf("generation type %q does not reference canonical schema: %q", name, property.Ref)
		}
	}
}
