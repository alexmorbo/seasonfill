package mediastore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// keyPrefix is the layout version prefix. Bumping the prefix lets us
// migrate the layout in the future without rewriting existing keys.
const keyPrefix = "media/v1"

// Key returns the content-addressed object key for sourceURL and ext.
// The shape is media/v1/{sha256[:2]}/{sha256}.{ext}. The 2-char shard
// keeps FS-mode directories under 256 entries and gives S3 prefix
// parallelism. ext is normalised to lowercase without a leading dot;
// an empty ext yields a key without an extension.
//
// Two URLs that differ only in the size variant ("…/w342/abc.jpg" vs
// "…/original/abc.jpg") produce different keys — they are different
// objects.
func Key(sourceURL, ext string) string {
	sum := sha256.Sum256([]byte(sourceURL))
	hash := hex.EncodeToString(sum[:])
	shard := hash[:2]
	cleanExt := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	if cleanExt == "" {
		return fmt.Sprintf("%s/%s/%s", keyPrefix, shard, hash)
	}
	return fmt.Sprintf("%s/%s/%s.%s", keyPrefix, shard, hash, cleanExt)
}

// HashFromKey returns the sha256 hex string embedded in key, or the
// empty string when key does not match the layout. Used by the GET
// /media/{hash} endpoint (future story) to round-trip the hash without
// re-deriving it from the source URL.
func HashFromKey(key string) string {
	if !strings.HasPrefix(key, keyPrefix+"/") {
		return ""
	}
	rest := strings.TrimPrefix(key, keyPrefix+"/")
	// rest is "{shard}/{hash}[.{ext}]"
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	name := parts[1]
	if dot := strings.IndexByte(name, '.'); dot >= 0 {
		name = name[:dot]
	}
	if len(name) != sha256.Size*2 {
		return ""
	}
	return name
}
