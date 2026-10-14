package server_test

import "encoding/json"

type stringFields map[string]string

func (f *stringFields) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*f = make(stringFields, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			(*f)[k] = s
		}
	}
	return nil
}
