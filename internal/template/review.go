package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cortexnotes/cortex-sync/internal/domain"
)

const (
	MaxPayloadBytes = 1 << 20
	MaxFiles        = 200
	payloadFormat   = 1
)

type payloadFile struct {
	Path    *string `json:"path"`
	Content *string `json:"content"`
}

type payloadDocument struct {
	Format int           `json:"format"`
	Kind   string        `json:"kind"`
	Files  []payloadFile `json:"files"`
}

type reviewResult struct {
	FileCount int
	Note      string
}

func (r reviewResult) Approved() bool {
	return r.Note == ""
}

func reviewPayload(kind domain.TemplateKind, payload []byte) reviewResult {
	if len(payload) > MaxPayloadBytes {
		return reviewResult{Note: "payload exceeds 1 MiB"}
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var doc payloadDocument
	if err := decoder.Decode(&doc); err != nil {
		return reviewResult{Note: "payload is not a valid template document"}
	}
	if decoder.More() {
		return reviewResult{Note: "payload is not a valid template document"}
	}
	if doc.Format != payloadFormat {
		return reviewResult{Note: fmt.Sprintf("unsupported format %d", doc.Format)}
	}
	if doc.Kind != string(kind) {
		return reviewResult{Note: "payload kind does not match template kind"}
	}
	if len(doc.Files) == 0 {
		return reviewResult{Note: "template has no files"}
	}
	if len(doc.Files) > MaxFiles {
		return reviewResult{Note: fmt.Sprintf("template has more than %d files", MaxFiles)}
	}
	seen := make(map[string]struct{}, len(doc.Files))
	for _, file := range doc.Files {
		if file.Path == nil || file.Content == nil {
			return reviewResult{Note: "every file needs a path and content"}
		}
		if !safeTemplatePath(*file.Path) {
			return reviewResult{Note: fmt.Sprintf("unsafe file path %q", *file.Path)}
		}
		if _, dup := seen[*file.Path]; dup {
			return reviewResult{Note: fmt.Sprintf("duplicate file path %q", *file.Path)}
		}
		seen[*file.Path] = struct{}{}
	}
	return reviewResult{FileCount: len(doc.Files)}
}

func countFiles(kind domain.TemplateKind, payload []byte) int {
	return reviewPayload(kind, payload).FileCount
}

func safeTemplatePath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\\:") {
		return false
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".json")
}
