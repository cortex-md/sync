package template

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/apidocs/apidocstest"
)

func TestOpenAPIContract(t *testing.T) {
	apidocstest.AssertMatches(t, "Template", templateResponse{})
	apidocstest.AssertMatches(t, "PublicTemplate", publicTemplateResponse{})
	apidocstest.AssertMatches(t, "CreateTemplateRequest", createTemplateRequest{})
	apidocstest.AssertMatches(t, "TemplatePayload", payloadResponse{})
	apidocstest.AssertMatches(t, "ReplaceTemplatePayloadRequest", replacePayloadRequest{})
	apidocstest.AssertMatches(t, "UpdateTemplateRequest", updateTemplateRequest{})
	apidocstest.AssertMatches(t, "PublicTemplatePayload", publicPayloadResponse{})
}
