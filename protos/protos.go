// Package protos provide helper functions for manipulating Luther protos.
package protos

import (
	"encoding/json"
	"fmt"
	"strings"

	annotationspb "buf.build/gen/go/luthersystems/protos/protocolbuffers/go/annotations/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"gopkg.in/yaml.v3"
)

// yamlToJSON converts YAML bytes to JSON bytes.
func yamlToJSON(yamlBytes []byte) ([]byte, error) {
	var raw interface{}
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	// Normalize map keys
	normalized := normalizeKeys(raw)

	// Convert to JSON
	return json.Marshal(normalized)
}

// normalizeKeys recursively normalizes map keys (dashes → underscores).
func normalizeKeys(input interface{}) interface{} {
	switch v := input.(type) {
	case map[interface{}]interface{}: // YAML unmarshals maps into this type
	case map[string]interface{}: // Already a JSON-like map
		result := make(map[string]interface{}, len(v))
		for key, value := range v {
			strKey := fmt.Sprintf("%v", key)                      // Convert key to string
			normalizedKey := strings.ReplaceAll(strKey, "-", "_") // Normalize dashes
			result[normalizedKey] = normalizeKeys(value)          // Recursively normalize values
		}
		return result
	case []interface{}: // Handle lists
		for i, item := range v {
			v[i] = normalizeKeys(item)
		}
	}
	return input
}

// RemoveSensitiveFields replaces sensitive fields with their first letter followed by "****".
func RemoveSensitiveFields(msg proto.Message) proto.Message {
	msgReflect := msg.ProtoReflect()
	msgCopy := msgReflect.New() // Create a new copy

	msgReflect.Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		// Check if the field has the "sensitive" annotation
		if proto.GetExtension(fd.Options(), annotationspb.E_Sensitive).(bool) {
			strVal := value.String()
			if len(strVal) > 0 {
				// Replace with first letter + "****"
				sanitizedVal := string(strVal[0]) + "****"
				msgCopy.Set(fd, protoreflect.ValueOfString(sanitizedVal))
			}
			return true
		}
		// Copy non-sensitive fields as is
		msgCopy.Set(fd, value)
		return true
	})

	return msgCopy.Interface()
}
