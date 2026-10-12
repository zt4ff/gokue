package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/zt4ff/gokue/job"
)

var (
	// ErrNilJob is returned when a nil job is registered or encoded.
	ErrNilJob = errors.New("job cannot be nil")
	// ErrNameConflict is returned when a name is already bound to a different type.
	ErrNameConflict = errors.New("job name already registered for a different type")
	// ErrJobNotRegistered is returned when a job type or name is unknown to the codec.
	ErrJobNotRegistered = errors.New("job type is not registered")
	// ErrEmptyPayload is returned when decoding an empty payload.
	ErrEmptyPayload = errors.New("job payload is empty")
	// ErrNoSerializableFields is returned when a job would not round-trip
	// through JSON, for example because it has no exported fields.
	ErrNoSerializableFields = errors.New("job has no serializable fields")
)

// Codec serializes jobs to and from the payload stored in a JobRecord using
// JSON. Types must be registered before they can be encoded or decoded. The
// registry is per-Codec, so distinct stores can use independent codecs.
//
// JSON is used instead of gob to avoid gob's process-global registration and
// its panic-on-missing-type behaviour. The trade-off is that only exported
// fields (including exported fields of nested values) are serialized; types
// that need to hide fields should implement json.Marshaler and
// json.Unmarshaler.
type Codec struct {
	mu    sync.RWMutex
	types map[job.Name]reflect.Type
}

// NewCodec returns an empty codec.
func NewCodec() *Codec {
	return &Codec{types: make(map[job.Name]reflect.Type)}
}

// Register binds name to the concrete type of sample so that it can later be
// encoded and decoded. When name is empty it is derived via job.NameOf.
//
// Register fails loudly when sample cannot round-trip through JSON, returning
// ErrNoSerializableFields, so misconfigured jobs are caught at startup rather
// than silently persisting empty payloads.
func (c *Codec) Register(name job.Name, sample job.Job) error {
	if sample == nil {
		return ErrNilJob
	}
	if name == "" {
		name = job.Name(job.NameOf(sample))
	}

	typ, err := serializableType(sample)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.types[name]; ok {
		if existing != typ {
			return fmt.Errorf("%w: %q", ErrNameConflict, name)
		}
		return nil
	}

	c.types[name] = typ
	return nil
}

// Encode serializes j and returns the name it is registered under.
func (c *Codec) Encode(j job.Job) (job.Name, []byte, error) {
	if j == nil {
		return "", nil, ErrNilJob
	}

	name := job.Name(job.NameOf(j))
	c.mu.RLock()
	typ, ok := c.types[name]
	c.mu.RUnlock()
	if !ok {
		return "", nil, fmt.Errorf("%w: %q", ErrJobNotRegistered, name)
	}
	if reflect.TypeOf(j) != typ {
		return "", nil, fmt.Errorf("%w: %q", ErrJobNotRegistered, name)
	}

	payload, err := json.Marshal(j)
	if err != nil {
		return "", nil, fmt.Errorf("encode job %q: %w", name, err)
	}

	return name, payload, nil
}

// Decode reconstructs a job previously encoded under name.
func (c *Codec) Decode(name job.Name, payload []byte) (job.Job, error) {
	if len(payload) == 0 {
		return nil, ErrEmptyPayload
	}

	c.mu.RLock()
	typ, ok := c.types[name]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrJobNotRegistered, name)
	}

	value := newValue(typ)
	if err := json.Unmarshal(payload, value.Interface()); err != nil {
		return nil, fmt.Errorf("decode job %q: %w", name, err)
	}

	j, ok := value.Interface().(job.Job)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrJobNotRegistered, name)
	}

	return j, nil
}

// serializableType validates that sample round-trips through JSON and returns
// its concrete type.
func serializableType(sample job.Job) (reflect.Type, error) {
	typ := reflect.TypeOf(sample)

	payload, err := json.Marshal(sample)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoSerializableFields, err)
	}

	// A struct with fields that marshals to an empty object (or null) has no
	// exported fields, which means every value would be silently dropped.
	if base := derefType(typ); base.Kind() == reflect.Struct && base.NumField() > 0 {
		if string(payload) == "{}" || string(payload) == "null" {
			return nil, fmt.Errorf("%w: %s", ErrNoSerializableFields, typ)
		}
	}

	return typ, nil
}

// newValue returns a new value of typ that implements job.Job: a pointer to the
// underlying type when typ is not already a pointer.
func newValue(typ reflect.Type) reflect.Value {
	if typ.Kind() == reflect.Ptr {
		return reflect.New(typ.Elem())
	}
	return reflect.New(typ)
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}
