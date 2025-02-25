package protos

import (
	"testing"

	cnpb "buf.build/gen/go/luthersystems/protos/protocolbuffers/go/connectors/v1"
	"github.com/stretchr/testify/assert"
)

func TestRemoveSensitiveFields(t *testing.T) {
	// Create a sample CamundaStartConfig with sensitive fields
	config := &cnpb.CamundaStartConfig{
		GatewayUrl: "https://camunda.example.com",
		Username:   "admin",
		Password:   "supersecret",
		ApiToken:   "token123",
	}

	// Apply the sanitization function
	sanitized := RemoveSensitiveFields(config).(*cnpb.CamundaStartConfig)

	// Ensure non-sensitive fields remain
	assert.Equal(t, "https://camunda.example.com", sanitized.GetGatewayUrl(), "Gateway URL should remain")
	assert.Equal(t, "admin", sanitized.GetUsername(), "Username should remain")

	// Ensure sensitive fields are removed (zero values)
	assert.Equal(t, "s****", sanitized.GetPassword(), "Password should be sanitized")
	assert.Equal(t, "t****", sanitized.GetApiToken(), "API token should be sanitized")
}
