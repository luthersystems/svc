// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.5
// 	protoc        (unknown)
// source: proto/hello/v1/hello.proto

package hellov1

import (
	v1 "buf.build/gen/go/luthersystems/protos/protocolbuffers/go/common/v1"
	_ "google.golang.org/genproto/googleapis/api/annotations"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	reflect "reflect"
	sync "sync"
	unsafe "unsafe"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type HelloRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Name          string                 `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *HelloRequest) Reset() {
	*x = HelloRequest{}
	mi := &file_proto_hello_v1_hello_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *HelloRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*HelloRequest) ProtoMessage() {}

func (x *HelloRequest) ProtoReflect() protoreflect.Message {
	mi := &file_proto_hello_v1_hello_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use HelloRequest.ProtoReflect.Descriptor instead.
func (*HelloRequest) Descriptor() ([]byte, []int) {
	return file_proto_hello_v1_hello_proto_rawDescGZIP(), []int{0}
}

func (x *HelloRequest) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

type HelloResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Exception     *v1.Exception          `protobuf:"bytes,1,opt,name=exception,proto3" json:"exception,omitempty"`
	Greeting      string                 `protobuf:"bytes,2,opt,name=greeting,proto3" json:"greeting,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *HelloResponse) Reset() {
	*x = HelloResponse{}
	mi := &file_proto_hello_v1_hello_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *HelloResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*HelloResponse) ProtoMessage() {}

func (x *HelloResponse) ProtoReflect() protoreflect.Message {
	mi := &file_proto_hello_v1_hello_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use HelloResponse.ProtoReflect.Descriptor instead.
func (*HelloResponse) Descriptor() ([]byte, []int) {
	return file_proto_hello_v1_hello_proto_rawDescGZIP(), []int{1}
}

func (x *HelloResponse) GetException() *v1.Exception {
	if x != nil {
		return x.Exception
	}
	return nil
}

func (x *HelloResponse) GetGreeting() string {
	if x != nil {
		return x.Greeting
	}
	return ""
}

type UseDepTxResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Exception     *v1.Exception          `protobuf:"bytes,1,opt,name=exception,proto3" json:"exception,omitempty"`
	OldTxId       string                 `protobuf:"bytes,2,opt,name=old_tx_id,json=oldTxId,proto3" json:"old_tx_id,omitempty"`
	NewTxId       string                 `protobuf:"bytes,3,opt,name=new_tx_id,json=newTxId,proto3" json:"new_tx_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UseDepTxResponse) Reset() {
	*x = UseDepTxResponse{}
	mi := &file_proto_hello_v1_hello_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UseDepTxResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UseDepTxResponse) ProtoMessage() {}

func (x *UseDepTxResponse) ProtoReflect() protoreflect.Message {
	mi := &file_proto_hello_v1_hello_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UseDepTxResponse.ProtoReflect.Descriptor instead.
func (*UseDepTxResponse) Descriptor() ([]byte, []int) {
	return file_proto_hello_v1_hello_proto_rawDescGZIP(), []int{2}
}

func (x *UseDepTxResponse) GetException() *v1.Exception {
	if x != nil {
		return x.Exception
	}
	return nil
}

func (x *UseDepTxResponse) GetOldTxId() string {
	if x != nil {
		return x.OldTxId
	}
	return ""
}

func (x *UseDepTxResponse) GetNewTxId() string {
	if x != nil {
		return x.NewTxId
	}
	return ""
}

type ConfigResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Environment   string                 `protobuf:"bytes,1,opt,name=environment,proto3" json:"environment,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ConfigResponse) Reset() {
	*x = ConfigResponse{}
	mi := &file_proto_hello_v1_hello_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ConfigResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConfigResponse) ProtoMessage() {}

func (x *ConfigResponse) ProtoReflect() protoreflect.Message {
	mi := &file_proto_hello_v1_hello_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConfigResponse.ProtoReflect.Descriptor instead.
func (*ConfigResponse) Descriptor() ([]byte, []int) {
	return file_proto_hello_v1_hello_proto_rawDescGZIP(), []int{3}
}

func (x *ConfigResponse) GetEnvironment() string {
	if x != nil {
		return x.Environment
	}
	return ""
}

var File_proto_hello_v1_hello_proto protoreflect.FileDescriptor

var file_proto_hello_v1_hello_proto_rawDesc = string([]byte{
	0x0a, 0x1a, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x68, 0x65, 0x6c, 0x6c, 0x6f, 0x2f, 0x76, 0x31,
	0x2f, 0x68, 0x65, 0x6c, 0x6c, 0x6f, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x08, 0x68, 0x65,
	0x6c, 0x6c, 0x6f, 0x2e, 0x76, 0x31, 0x1a, 0x19, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2f, 0x76,
	0x31, 0x2f, 0x65, 0x78, 0x63, 0x65, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x1a, 0x1c, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x61, 0x70, 0x69, 0x2f, 0x61, 0x6e,
	0x6e, 0x6f, 0x74, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a,
	0x1b, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66,
	0x2f, 0x65, 0x6d, 0x70, 0x74, 0x79, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x22, 0x0a, 0x0c,
	0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x12, 0x0a, 0x04,
	0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65,
	0x22, 0x5f, 0x0a, 0x0d, 0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73,
	0x65, 0x12, 0x32, 0x0a, 0x09, 0x65, 0x78, 0x63, 0x65, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2e, 0x76, 0x31,
	0x2e, 0x45, 0x78, 0x63, 0x65, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x52, 0x09, 0x65, 0x78, 0x63, 0x65,
	0x70, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x1a, 0x0a, 0x08, 0x67, 0x72, 0x65, 0x65, 0x74, 0x69, 0x6e,
	0x67, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08, 0x67, 0x72, 0x65, 0x65, 0x74, 0x69, 0x6e,
	0x67, 0x22, 0x7e, 0x0a, 0x10, 0x55, 0x73, 0x65, 0x44, 0x65, 0x70, 0x54, 0x78, 0x52, 0x65, 0x73,
	0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x32, 0x0a, 0x09, 0x65, 0x78, 0x63, 0x65, 0x70, 0x74, 0x69,
	0x6f, 0x6e, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x6f,
	0x6e, 0x2e, 0x76, 0x31, 0x2e, 0x45, 0x78, 0x63, 0x65, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x52, 0x09,
	0x65, 0x78, 0x63, 0x65, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x1a, 0x0a, 0x09, 0x6f, 0x6c, 0x64,
	0x5f, 0x74, 0x78, 0x5f, 0x69, 0x64, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x6f, 0x6c,
	0x64, 0x54, 0x78, 0x49, 0x64, 0x12, 0x1a, 0x0a, 0x09, 0x6e, 0x65, 0x77, 0x5f, 0x74, 0x78, 0x5f,
	0x69, 0x64, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x6e, 0x65, 0x77, 0x54, 0x78, 0x49,
	0x64, 0x22, 0x32, 0x0a, 0x0e, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x52, 0x65, 0x73, 0x70, 0x6f,
	0x6e, 0x73, 0x65, 0x12, 0x20, 0x0a, 0x0b, 0x65, 0x6e, 0x76, 0x69, 0x72, 0x6f, 0x6e, 0x6d, 0x65,
	0x6e, 0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x65, 0x6e, 0x76, 0x69, 0x72, 0x6f,
	0x6e, 0x6d, 0x65, 0x6e, 0x74, 0x32, 0x82, 0x02, 0x0a, 0x0c, 0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x53,
	0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x51, 0x0a, 0x08, 0x53, 0x61, 0x79, 0x48, 0x65, 0x6c,
	0x6c, 0x6f, 0x12, 0x16, 0x2e, 0x68, 0x65, 0x6c, 0x6c, 0x6f, 0x2e, 0x76, 0x31, 0x2e, 0x48, 0x65,
	0x6c, 0x6c, 0x6f, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x17, 0x2e, 0x68, 0x65, 0x6c,
	0x6c, 0x6f, 0x2e, 0x76, 0x31, 0x2e, 0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x52, 0x65, 0x73, 0x70, 0x6f,
	0x6e, 0x73, 0x65, 0x22, 0x14, 0x82, 0xd3, 0xe4, 0x93, 0x02, 0x0e, 0x3a, 0x01, 0x2a, 0x22, 0x09,
	0x2f, 0x76, 0x31, 0x2f, 0x68, 0x65, 0x6c, 0x6c, 0x6f, 0x12, 0x48, 0x0a, 0x04, 0x50, 0x69, 0x6e,
	0x67, 0x12, 0x16, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x62, 0x75, 0x66, 0x2e, 0x45, 0x6d, 0x70, 0x74, 0x79, 0x1a, 0x16, 0x2e, 0x67, 0x6f, 0x6f, 0x67,
	0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x45, 0x6d, 0x70, 0x74,
	0x79, 0x22, 0x10, 0x82, 0xd3, 0xe4, 0x93, 0x02, 0x0a, 0x12, 0x08, 0x2f, 0x76, 0x31, 0x2f, 0x70,
	0x69, 0x6e, 0x67, 0x12, 0x55, 0x0a, 0x08, 0x55, 0x73, 0x65, 0x44, 0x65, 0x70, 0x54, 0x78, 0x12,
	0x16, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75,
	0x66, 0x2e, 0x45, 0x6d, 0x70, 0x74, 0x79, 0x1a, 0x1a, 0x2e, 0x68, 0x65, 0x6c, 0x6c, 0x6f, 0x2e,
	0x76, 0x31, 0x2e, 0x55, 0x73, 0x65, 0x44, 0x65, 0x70, 0x54, 0x78, 0x52, 0x65, 0x73, 0x70, 0x6f,
	0x6e, 0x73, 0x65, 0x22, 0x15, 0x82, 0xd3, 0xe4, 0x93, 0x02, 0x0f, 0x3a, 0x01, 0x2a, 0x22, 0x0a,
	0x2f, 0x76, 0x31, 0x2f, 0x64, 0x65, 0x70, 0x5f, 0x74, 0x78, 0x42, 0xa4, 0x01, 0x0a, 0x0c, 0x63,
	0x6f, 0x6d, 0x2e, 0x68, 0x65, 0x6c, 0x6c, 0x6f, 0x2e, 0x76, 0x31, 0x42, 0x0a, 0x48, 0x65, 0x6c,
	0x6c, 0x6f, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x47, 0x67, 0x69, 0x74, 0x68, 0x75,
	0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x6c, 0x75, 0x74, 0x68, 0x65, 0x72, 0x73, 0x79, 0x73, 0x74,
	0x65, 0x6d, 0x73, 0x2f, 0x73, 0x76, 0x63, 0x2f, 0x6f, 0x72, 0x61, 0x63, 0x6c, 0x65, 0x2f, 0x74,
	0x65, 0x73, 0x74, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x2f, 0x67,
	0x6f, 0x2f, 0x68, 0x65, 0x6c, 0x6c, 0x6f, 0x2f, 0x76, 0x31, 0x3b, 0x68, 0x65, 0x6c, 0x6c, 0x6f,
	0x76, 0x31, 0xa2, 0x02, 0x03, 0x48, 0x58, 0x58, 0xaa, 0x02, 0x08, 0x48, 0x65, 0x6c, 0x6c, 0x6f,
	0x2e, 0x56, 0x31, 0xca, 0x02, 0x08, 0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x5c, 0x56, 0x31, 0xe2, 0x02,
	0x14, 0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x5c, 0x56, 0x31, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74,
	0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x09, 0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x3a, 0x3a, 0x56,
	0x31, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
})

var (
	file_proto_hello_v1_hello_proto_rawDescOnce sync.Once
	file_proto_hello_v1_hello_proto_rawDescData []byte
)

func file_proto_hello_v1_hello_proto_rawDescGZIP() []byte {
	file_proto_hello_v1_hello_proto_rawDescOnce.Do(func() {
		file_proto_hello_v1_hello_proto_rawDescData = protoimpl.X.CompressGZIP(unsafe.Slice(unsafe.StringData(file_proto_hello_v1_hello_proto_rawDesc), len(file_proto_hello_v1_hello_proto_rawDesc)))
	})
	return file_proto_hello_v1_hello_proto_rawDescData
}

var file_proto_hello_v1_hello_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_proto_hello_v1_hello_proto_goTypes = []any{
	(*HelloRequest)(nil),     // 0: hello.v1.HelloRequest
	(*HelloResponse)(nil),    // 1: hello.v1.HelloResponse
	(*UseDepTxResponse)(nil), // 2: hello.v1.UseDepTxResponse
	(*ConfigResponse)(nil),   // 3: hello.v1.ConfigResponse
	(*v1.Exception)(nil),     // 4: common.v1.Exception
	(*emptypb.Empty)(nil),    // 5: google.protobuf.Empty
}
var file_proto_hello_v1_hello_proto_depIdxs = []int32{
	4, // 0: hello.v1.HelloResponse.exception:type_name -> common.v1.Exception
	4, // 1: hello.v1.UseDepTxResponse.exception:type_name -> common.v1.Exception
	0, // 2: hello.v1.HelloService.SayHello:input_type -> hello.v1.HelloRequest
	5, // 3: hello.v1.HelloService.Ping:input_type -> google.protobuf.Empty
	5, // 4: hello.v1.HelloService.UseDepTx:input_type -> google.protobuf.Empty
	1, // 5: hello.v1.HelloService.SayHello:output_type -> hello.v1.HelloResponse
	5, // 6: hello.v1.HelloService.Ping:output_type -> google.protobuf.Empty
	2, // 7: hello.v1.HelloService.UseDepTx:output_type -> hello.v1.UseDepTxResponse
	5, // [5:8] is the sub-list for method output_type
	2, // [2:5] is the sub-list for method input_type
	2, // [2:2] is the sub-list for extension type_name
	2, // [2:2] is the sub-list for extension extendee
	0, // [0:2] is the sub-list for field type_name
}

func init() { file_proto_hello_v1_hello_proto_init() }
func file_proto_hello_v1_hello_proto_init() {
	if File_proto_hello_v1_hello_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: unsafe.Slice(unsafe.StringData(file_proto_hello_v1_hello_proto_rawDesc), len(file_proto_hello_v1_hello_proto_rawDesc)),
			NumEnums:      0,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_proto_hello_v1_hello_proto_goTypes,
		DependencyIndexes: file_proto_hello_v1_hello_proto_depIdxs,
		MessageInfos:      file_proto_hello_v1_hello_proto_msgTypes,
	}.Build()
	File_proto_hello_v1_hello_proto = out.File
	file_proto_hello_v1_hello_proto_goTypes = nil
	file_proto_hello_v1_hello_proto_depIdxs = nil
}
