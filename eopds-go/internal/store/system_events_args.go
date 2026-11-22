package store

import "encoding/json"

// marshalArgs сериализует подстановки события в JSON.
func marshalArgs(m map[string]string) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// unmarshalArgs разбирает JSON-подстановки; при ошибке — пустая карта.
func unmarshalArgs(s string) map[string]string {
	if s == "" {
		return map[string]string{}
	}
	m := map[string]string{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]string{}
	}
	return m
}
