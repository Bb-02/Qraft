package activities

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
)

// closedModelJSONSchemaV2 describes the same JSON field names and shapes as the
// typed decoder. It is prompt material, not a replacement for strict decoding
// or the domain's semantic checks. Explicit omissions are server-owned fields.
func closedModelJSONSchemaV2(target reflect.Type, omitted map[reflect.Type][]string) (string, error) {
	definitions := make(map[string]interface{})
	owners := make(map[string]reflect.Type)
	var describe func(reflect.Type) (map[string]interface{}, error)
	describe = func(typ reflect.Type) (map[string]interface{}, error) {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		switch typ.Kind() {
		case reflect.Struct:
			name := typ.Name()
			if name == "" {
				return nil, fmt.Errorf("model JSON schema requires named structs")
			}
			if previous, exists := owners[name]; exists {
				if previous != typ {
					return nil, fmt.Errorf("model JSON schema type name collision: %s", name)
				}
				return map[string]interface{}{"$ref": "#/$defs/" + name}, nil
			}
			// Reserve before descending so recursive expression ASTs stay bounded.
			owners[name] = typ
			definitions[name] = nil
			properties := make(map[string]interface{})
			required := make([]string, 0, typ.NumField())
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if field.PkgPath != "" {
					continue
				}
				tag := strings.Split(field.Tag.Get("json"), ",")
				fieldName := tag[0]
				if fieldName == "-" {
					continue
				}
				if fieldName == "" {
					fieldName = field.Name
				}
				skip := false
				for _, excluded := range omitted[typ] {
					skip = skip || excluded == fieldName
				}
				if skip {
					continue
				}
				property, err := describe(field.Type)
				if err != nil {
					return nil, fmt.Errorf("%s.%s: %w", name, fieldName, err)
				}
				properties[fieldName] = property
				optional := false
				for _, option := range tag[1:] {
					optional = optional || option == "omitempty"
				}
				if !optional {
					required = append(required, fieldName)
				}
			}
			definitions[name] = map[string]interface{}{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
			return map[string]interface{}{"$ref": "#/$defs/" + name}, nil
		case reflect.Slice, reflect.Array:
			item, err := describe(typ.Elem())
			return map[string]interface{}{"type": "array", "items": item}, err
		case reflect.String:
			return map[string]interface{}{"type": "string"}, nil
		case reflect.Bool:
			return map[string]interface{}{"type": "boolean"}, nil
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return map[string]interface{}{"type": "integer"}, nil
		case reflect.Float32, reflect.Float64:
			return map[string]interface{}{"type": "number"}, nil
		default:
			return nil, fmt.Errorf("unsupported model JSON schema kind %s", typ.Kind())
		}
	}
	root, err := describe(target)
	if err != nil {
		return "", err
	}
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	root["$defs"] = definitions
	encoded, err := json.Marshal(root)
	return string(encoded), err
}

func modelResponseTruncatedV2(response *llm.Response) bool {
	if response == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(response.StopReason)) {
	case "max_tokens", "length", "max_output_tokens", "token_limit":
		return true
	default:
		return false
	}
}
