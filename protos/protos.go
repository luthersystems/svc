// Package protos provide helper functions for manipulating Luther protos.
package protos

import (
	annotationspb "buf.build/gen/go/luthersystems/protos/protocolbuffers/go/annotations/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func RemoveSensitiveFields(msg proto.Message) proto.Message {
	return removeSensitive(msg.ProtoReflect()).Interface()
}

func removeSensitive(msg protoreflect.Message) protoreflect.Message {
	msgCopy := msg.New()

	msg.Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if fd.IsList() {
			// Handle repeated fields
			list := msgCopy.NewField(fd).List()
			for i := 0; i < value.List().Len(); i++ {
				item := value.List().Get(i)
				if fd.Kind() == protoreflect.MessageKind {
					list.Append(protoreflect.ValueOfMessage(
						removeSensitive(item.Message())))
				} else {
					list.Append(item)
				}
			}
			msgCopy.Set(fd, protoreflect.ValueOfList(list))
			return true
		}

		if fd.IsMap() {
			// Handle map fields
			mapVal := msgCopy.NewField(fd).Map()
			value.Map().Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
				if fd.MapValue().Kind() == protoreflect.MessageKind {
					mapVal.Set(k, protoreflect.ValueOfMessage(removeSensitive(v.Message())))
				} else {
					mapVal.Set(k, v)
				}
				return true
			})
			msgCopy.Set(fd, protoreflect.ValueOfMap(mapVal))
			return true
		}

		if fd.Kind() == protoreflect.MessageKind {
			// Recurse into sub-message
			msgCopy.Set(fd, protoreflect.ValueOfMessage(removeSensitive(value.Message())))
			return true
		}

		// Handle scalar sensitive fields
		if extVal, ok := proto.GetExtension(fd.Options(), annotationspb.E_Sensitive).(bool); ok && extVal {
			strVal := value.String()
			if len(strVal) > 0 {
				sanitized := string(strVal[0]) + "****"
				msgCopy.Set(fd, protoreflect.ValueOfString(sanitized))
			}
			return true
		}

		// Normal scalar field
		msgCopy.Set(fd, value)
		return true
	})

	return msgCopy
}
