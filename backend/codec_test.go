package backend

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/zt4ff/gokue/job"
)

type codecJob struct {
	To   string `json:"to"`
	Body string `json:"body"`
}

func (codecJob) Process(context.Context) error { return nil }

type namedCodecJob struct{}

func (namedCodecJob) Process(context.Context) error { return nil }
func (namedCodecJob) Name() string                  { return "explicit-name" }

type unexportedCodecJob struct{ secret string }

func (*unexportedCodecJob) Process(context.Context) error { return nil }

type funcCodecJob func(context.Context) error

func (f funcCodecJob) Process(ctx context.Context) error { return f(ctx) }

type marshalerCodecJob struct{ secret string }

func (marshalerCodecJob) Process(context.Context) error { return nil }

func (j marshalerCodecJob) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"secret": j.secret})
}

func (j *marshalerCodecJob) UnmarshalJSON(data []byte) error {
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	j.secret = raw["secret"]
	return nil
}

type dupCodecJobA struct{ A string }

func (dupCodecJobA) Process(context.Context) error { return nil }

type dupCodecJobB struct{ B string }

func (dupCodecJobB) Process(context.Context) error { return nil }

func TestCodecRoundTripValue(t *testing.T) {
	t.Parallel()

	c := NewCodec()
	if err := c.Register("", codecJob{}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	name, payload, err := c.Encode(codecJob{To: "a@b.com", Body: "hello"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if name != "codecJob" {
		t.Fatalf("name = %q, want codecJob", name)
	}

	got, err := c.Decode(name, payload)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	decoded, ok := got.(*codecJob)
	if !ok {
		t.Fatalf("decoded type = %T, want *codecJob", got)
	}
	if decoded.To != "a@b.com" || decoded.Body != "hello" {
		t.Fatalf("decoded = %+v", *decoded)
	}
}

func TestCodecRoundTripPointer(t *testing.T) {
	t.Parallel()

	c := NewCodec()
	if err := c.Register("", &codecJob{}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	name, payload, err := c.Encode(&codecJob{To: "x@y.z", Body: "ptr"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, err := c.Decode(name, payload)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	decoded, ok := got.(*codecJob)
	if !ok {
		t.Fatalf("decoded type = %T, want *codecJob", got)
	}
	if decoded.To != "x@y.z" || decoded.Body != "ptr" {
		t.Fatalf("decoded = %+v", *decoded)
	}
}

func TestCodecRegisterNamed(t *testing.T) {
	t.Parallel()

	c := NewCodec()
	if err := c.Register("", namedCodecJob{}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	name, _, err := c.Encode(namedCodecJob{})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if name != "explicit-name" {
		t.Fatalf("name = %q, want explicit-name", name)
	}
}

func TestCodecRegisterErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		sample func() job.Job
		want   error
	}{
		{
			name:   "nil job",
			sample: func() job.Job { return nil },
			want:   ErrNilJob,
		},
		{
			name:   "unexported fields",
			sample: func() job.Job { return &unexportedCodecJob{} },
			want:   ErrNoSerializableFields,
		},
		{
			name: "func kind",
			sample: func() job.Job {
				return funcCodecJob(func(context.Context) error { return nil })
			},
			want: ErrNoSerializableFields,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := NewCodec()
			if err := c.Register("", tt.sample()); !errors.Is(err, tt.want) {
				t.Fatalf("Register error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCodecNameConflict(t *testing.T) {
	t.Parallel()

	c := NewCodec()
	if err := c.Register("dup", dupCodecJobA{}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := c.Register("dup", dupCodecJobA{}); err != nil {
		t.Fatalf("idempotent Register: %v", err)
	}
	if err := c.Register("dup", dupCodecJobB{}); !errors.Is(err, ErrNameConflict) {
		t.Fatalf("conflicting Register error = %v, want ErrNameConflict", err)
	}
}

func TestCodecValueAndPointerConflict(t *testing.T) {
	t.Parallel()

	c := NewCodec()
	if err := c.Register("", codecJob{}); err != nil {
		t.Fatalf("Register value: %v", err)
	}
	if err := c.Register("", &codecJob{}); !errors.Is(err, ErrNameConflict) {
		t.Fatalf("Register pointer error = %v, want ErrNameConflict", err)
	}
}

func TestCodecEncodeErrors(t *testing.T) {
	t.Parallel()

	c := NewCodec()
	if _, _, err := c.Encode(nil); !errors.Is(err, ErrNilJob) {
		t.Fatalf("Encode(nil) error = %v, want ErrNilJob", err)
	}

	if _, _, err := c.Encode(codecJob{}); !errors.Is(err, ErrJobNotRegistered) {
		t.Fatalf("Encode(unregistered) error = %v, want ErrJobNotRegistered", err)
	}
}

func TestCodecDecodeErrors(t *testing.T) {
	t.Parallel()

	c := NewCodec()
	if err := c.Register("", codecJob{}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := c.Decode("codecJob", nil); !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("Decode(empty) error = %v, want ErrEmptyPayload", err)
	}
	if _, err := c.Decode("unknown", []byte(`{}`)); !errors.Is(err, ErrJobNotRegistered) {
		t.Fatalf("Decode(unknown) error = %v, want ErrJobNotRegistered", err)
	}
}

func TestCodecMarshalerEscapeHatch(t *testing.T) {
	t.Parallel()

	c := NewCodec()
	if err := c.Register("", marshalerCodecJob{secret: "s3cr3t"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	name, payload, err := c.Encode(marshalerCodecJob{secret: "s3cr3t"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, err := c.Decode(name, payload)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	decoded, ok := got.(*marshalerCodecJob)
	if !ok {
		t.Fatalf("decoded type = %T, want *marshalerCodecJob", got)
	}
	if decoded.secret != "s3cr3t" {
		t.Fatalf("secret = %q, want s3cr3t", decoded.secret)
	}
}
