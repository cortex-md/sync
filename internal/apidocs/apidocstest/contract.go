package apidocstest

import (
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/apidocs"
	"go.yaml.in/yaml/v3"
)

type schema struct {
	Ref        string         `yaml:"$ref"`
	Properties map[string]any `yaml:"properties"`
	AllOf      []schema       `yaml:"allOf"`
	Extra      map[string]any `yaml:",inline"`
}

var (
	loadOnce sync.Once
	schemas  map[string]schema
	loadErr  error
)

func load() (map[string]schema, error) {
	loadOnce.Do(func() {
		var doc struct {
			Components struct {
				Schemas map[string]schema `yaml:"schemas"`
			} `yaml:"components"`
		}
		loadErr = yaml.Unmarshal(apidocs.Spec, &doc)
		schemas = doc.Components.Schemas
	})
	return schemas, loadErr
}

func schemaFields(all map[string]schema, s schema, seen map[string]bool) map[string]bool {
	fields := map[string]bool{}
	if s.Ref != "" {
		name := strings.TrimPrefix(s.Ref, "#/components/schemas/")
		if seen[name] {
			return fields
		}
		seen[name] = true
		return schemaFields(all, all[name], seen)
	}
	for name := range s.Properties {
		fields[name] = true
	}
	for _, part := range s.AllOf {
		for name := range schemaFields(all, part, seen) {
			fields[name] = true
		}
	}
	return fields
}

func structFields(t reflect.Type) map[string]bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	fields := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" {
			for nested := range structFields(field.Type) {
				fields[nested] = true
			}
			continue
		}
		if !field.IsExported() && tag == "" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = true
	}
	return fields
}

func AssertMatches(t testing.TB, schemaName string, value any) {
	t.Helper()
	all, err := load()
	if err != nil {
		t.Fatalf("parse openapi spec: %v", err)
	}
	s, ok := all[schemaName]
	if !ok {
		t.Errorf("schema %s is not defined in internal/apidocs/openapi.yaml", schemaName)
		return
	}
	documented := schemaFields(all, s, map[string]bool{schemaName: true})
	actual := structFields(reflect.TypeOf(value))
	var missing, stale []string
	for name := range actual {
		if !documented[name] {
			missing = append(missing, name)
		}
	}
	for name := range documented {
		if !actual[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 || len(stale) > 0 {
		t.Errorf("schema %s drifted from %T: undocumented fields %v, documented but absent %v", schemaName, value, missing, stale)
	}
}
