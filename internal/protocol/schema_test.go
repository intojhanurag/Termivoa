package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
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

func TestGenerationIndexReferencesCanonicalSchemas(t *testing.T) {
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
		if !strings.HasPrefix(property.Ref, "envelope.schema.json#/$defs/") &&
			!strings.HasPrefix(property.Ref, "ipc.schema.json#/$defs/") &&
			!strings.HasPrefix(property.Ref, "broker-rpc.schema.json#/$defs/") &&
			!strings.HasPrefix(property.Ref, "http-api.schema.json#/$defs/") &&
			!strings.HasPrefix(property.Ref, "gateway-rpc.schema.json#/$defs/") &&
			!strings.HasPrefix(property.Ref, "admin-rpc.schema.json#/$defs/") {
			t.Fatalf("generation type %q does not reference canonical schema: %q", name, property.Ref)
		}
	}
}

func TestCanonicalBrokerRPCSchemaListsEveryMessageExactlyOnce(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate broker RPC schema test")
	}
	schemaPath := filepath.Join(filepath.Dir(source), "..", "..", "protocol", "v1", "broker-rpc.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Schema string `json:"$schema"`
		OneOf  []struct {
			Ref string `json:"$ref"`
		} `json:"oneOf"`
		Defs struct {
			MessageType struct {
				Enum []string `json:"enum"`
			} `json:"messageType"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Schema != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("broker RPC schema draft = %q", schema.Schema)
	}
	want := make([]string, 0, len(brokerRPCRequestTypes)+len(brokerRPCResponseTypes))
	for messageType := range brokerRPCRequestTypes {
		want = append(want, string(messageType))
		if brokerRPCResponseTypes[messageType] {
			t.Fatalf("broker RPC message %q is both request and response", messageType)
		}
	}
	for messageType := range brokerRPCResponseTypes {
		want = append(want, string(messageType))
	}
	sort.Strings(want)
	got := append([]string(nil), schema.Defs.MessageType.Enum...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) || len(schema.OneOf) != len(want) {
		t.Fatalf("broker RPC schema messages = %q, validators = %q, oneOf = %d", got, want, len(schema.OneOf))
	}
	seen := make(map[string]bool, len(schema.OneOf))
	for _, entry := range schema.OneOf {
		if entry.Ref == "" || seen[entry.Ref] {
			t.Fatalf("broker RPC schema has missing or duplicate message ref %q", entry.Ref)
		}
		seen[entry.Ref] = true
	}
}

func TestCanonicalIPCSchemaListsHandshakeMessagesExactlyOnce(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate IPC schema test")
	}
	schemaPath := filepath.Join(filepath.Dir(source), "..", "..", "protocol", "v1", "ipc.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Schema string `json:"$schema"`
		OneOf  []struct {
			Ref string `json:"$ref"`
		} `json:"oneOf"`
		Defs struct {
			MessageType struct {
				Enum []string `json:"enum"`
			} `json:"messageType"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Schema != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("IPC schema draft = %q", schema.Schema)
	}
	want := []string{"ipc.error", "ipc.hello", "ipc.welcome"}
	got := append([]string(nil), schema.Defs.MessageType.Enum...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) || len(schema.OneOf) != len(want) {
		t.Fatalf("IPC schema messages = %q, oneOf = %d", got, len(schema.OneOf))
	}
	seen := make(map[string]bool, len(schema.OneOf))
	for _, entry := range schema.OneOf {
		if entry.Ref == "" || seen[entry.Ref] {
			t.Fatalf("IPC schema has missing or duplicate message ref %q", entry.Ref)
		}
		seen[entry.Ref] = true
	}
}
