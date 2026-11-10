package governance

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type s3WORMClientStub struct {
	putInput *s3.PutObjectInput
	object   []byte
	putErr   error
	getErr   error
}

func (s *s3WORMClientStub) PutObject(
	_ context.Context,
	input *s3.PutObjectInput,
	_ ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	s.putInput = input
	return &s3.PutObjectOutput{}, s.putErr
}

func (s *s3WORMClientStub) GetObject(
	_ context.Context,
	_ *s3.GetObjectInput,
	_ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(s.object))}, nil
}

func TestS3ObjectLockWORMUsesComplianceRetentionAndNoOverwrite(t *testing.T) {
	client := &s3WORMClientStub{}
	storage := &S3ObjectLockWORM{client: client, bucket: "audit-bucket"}
	retainUntil := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	object := []byte(`{"signed":"anchor"}`)
	if err := storage.PutImmutableObject(context.Background(), "company/date/audit-anchor.json", object, retainUntil); err != nil {
		t.Fatal(err)
	}
	if client.putInput == nil ||
		string(client.putInput.ObjectLockMode) != string(types.ObjectLockModeCompliance) ||
		awsString(client.putInput.IfNoneMatch) != "*" ||
		client.putInput.ObjectLockRetainUntilDate == nil ||
		!client.putInput.ObjectLockRetainUntilDate.Equal(retainUntil) {
		t.Fatalf("S3 Object Lock request does not enforce compliance retention and no-overwrite: %+v", client.putInput)
	}
	if err := storage.PutImmutableObject(context.Background(), "anchor", object, time.Now().Add(-time.Hour)); err == nil {
		t.Fatal("expected expired retention deadline to be rejected")
	}
}

func TestS3ObjectLockWORMReturnsStorageErrors(t *testing.T) {
	writeErr := errors.New("S3 write failed")
	storage := &S3ObjectLockWORM{client: &s3WORMClientStub{putErr: writeErr}, bucket: "audit-bucket"}
	err := storage.PutImmutableObject(context.Background(), "anchor", []byte("anchor"), time.Now().Add(time.Hour))
	if !errors.Is(err, writeErr) {
		t.Fatalf("S3 write error = %v, want underlying storage error", err)
	}
}

func awsString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
