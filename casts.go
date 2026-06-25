package playsql

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

// Caster converts a field between its Go representation and its database form.
// Register custom casts with RegisterCaster, then reference them on a field with
// the `play:"cast=<name>"` tag.
type Caster interface {
	// Decode sets dest (an addressable struct field) from the raw database value
	// (commonly []byte or string). A nil raw means SQL NULL.
	Decode(raw any, dest reflect.Value) error
	// Encode converts a field value to a database-storable value (for writes).
	Encode(field any) (any, error)
}

var (
	castersMu sync.RWMutex
	casters   = map[string]Caster{
		"json": jsonCaster{},
	}
)

// RegisterCaster registers a Caster under a cast name. Call it during init,
// before queries run. Re-registering a name replaces it.
func RegisterCaster(name string, c Caster) {
	castersMu.Lock()
	defer castersMu.Unlock()
	casters[name] = c
}

func casterFor(name string) (Caster, bool) {
	castersMu.RLock()
	defer castersMu.RUnlock()
	c, ok := casters[name]
	return c, ok
}

// jsonCaster is the built-in "json" cast: marshals/unmarshals the field.
type jsonCaster struct{}

func (jsonCaster) Decode(raw any, dest reflect.Value) error {
	var b []byte
	switch v := raw.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("playsql: json cast: cannot decode %T", raw)
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, dest.Addr().Interface())
}

func (jsonCaster) Encode(field any) (any, error) {
	b, err := json.Marshal(field)
	if err != nil {
		return nil, fmt.Errorf("playsql: json cast: %w", err)
	}
	return b, nil
}
