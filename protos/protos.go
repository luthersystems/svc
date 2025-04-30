// Package protos provide helper functions for manipulating Luther protos.
package protos

import (
	annotationspb "buf.build/gen/go/luthersystems/protos/protocolbuffers/go/annotations/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

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
