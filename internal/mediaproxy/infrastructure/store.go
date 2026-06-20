// Package mediastore is the abstraction over the seasonfill media
// blob store. Three implementations satisfy the same interface: an S3
// client (minio-go/v7) for the SeaweedFS-backed production deployment,
// a filesystem store with atomic writes for local development, and a
// null store that no-ops every call so the legacy poster-proxy code
// path keeps working when no store is configured.
//
// Layout is content-addressed — keys are derived from the sha256 of
// the upstream URL via Key. The store implementations are oblivious to
// the key shape; they treat keys as opaque object names.
package mediastore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// Mode selects the backing implementation returned by New.
type Mode string

const (
	// ModeOff disables the store. New returns a nullStore; every
	// operation returns ErrNotSupported. This is the default mode
	// so existing deployments stay on the legacy hotlink path.
	ModeOff Mode = "off"
	// ModeS3 uses minio-go/v7 against an S3-compatible endpoint.
	ModeS3 Mode = "s3"
	// ModeFS writes objects under a local directory using atomic
	// rename. Intended for local development and docker-compose.
	ModeFS Mode = "fs"
)

// ObjectInfo is the subset of object metadata callers need. It is
// implementation-agnostic — backends populate the fields they can.
type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
}

// Store is the contract every backend satisfies.
//
// Get returns a ReadCloser the caller MUST close, even on error from
// downstream reads. Put consumes r in full; size and contentType are
// hints — backends may compute their own. Stat returns ErrNotFound for
// missing objects. List invokes fn for each object under prefix;
// returning a non-nil error from fn aborts the walk and is propagated.
type Store interface {
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string, fn func(ObjectInfo) error) error
}

// Sentinel errors. Callers MUST use errors.Is to test these — backends
// wrap them with %w for context (path, request id, etc.).
var (
	// ErrNotFound is returned by Get / Stat when the key is absent.
	ErrNotFound = errors.New("mediastore: object not found")
	// ErrNotSupported is returned by every nullStore call and by
	// backends that cannot implement a given operation.
	ErrNotSupported = errors.New("mediastore: operation not supported in current mode")
	// ErrInvalidConfig is returned by New when cfg fails validation.
	ErrInvalidConfig = errors.New("mediastore: invalid config")
)

// Config is the bootstrap input that New consumes. Field semantics:
//
//   - Mode selects the backend; the zero value ("") is treated as
//     ModeOff so callers can pass an unset Config safely.
//   - S3 fields are read only when Mode == ModeS3. Region must be
//     non-empty (minio-go signs requests; SeaweedFS ignores it).
//   - FSPath is read only when Mode == ModeFS.
type Config struct {
	Mode   Mode
	S3     S3Config
	FSPath string
}

// S3Config carries the minio-go connection parameters. UseSSL=true
// implies https; false implies http. AccessKey / SecretKey are
// required when Mode == ModeS3.
type S3Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
	UseSSL    bool
}

// New returns the Store implementation matching cfg.Mode. An empty
// cfg.Mode is normalised to ModeOff. ctx is used only for the s3
// client's bucket-existence probe; it is not retained by the returned
// Store.
func New(ctx context.Context, cfg Config) (Store, error) {
	mode := cfg.Mode
	if mode == "" {
		mode = ModeOff
	}
	switch mode {
	case ModeOff:
		return newNullStore(), nil
	case ModeS3:
		if err := validateS3(cfg.S3); err != nil {
			return nil, err
		}
		return newS3Store(ctx, cfg.S3)
	case ModeFS:
		if cfg.FSPath == "" {
			return nil, fmt.Errorf("%w: fs path is empty", ErrInvalidConfig)
		}
		return newFSStore(cfg.FSPath)
	default:
		return nil, fmt.Errorf("%w: unknown mode %q", ErrInvalidConfig, mode)
	}
}

func validateS3(cfg S3Config) error {
	switch {
	case cfg.Endpoint == "":
		return fmt.Errorf("%w: s3 endpoint is empty", ErrInvalidConfig)
	case cfg.Bucket == "":
		return fmt.Errorf("%w: s3 bucket is empty", ErrInvalidConfig)
	case cfg.AccessKey == "":
		return fmt.Errorf("%w: s3 access key is empty", ErrInvalidConfig)
	case cfg.SecretKey == "":
		return fmt.Errorf("%w: s3 secret key is empty", ErrInvalidConfig)
	case cfg.Region == "":
		return fmt.Errorf("%w: s3 region is empty (SeaweedFS ignores it but minio-go signs with it)", ErrInvalidConfig)
	}
	return nil
}
