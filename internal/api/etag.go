package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/nm1996/beacon/internal/discovery"
)

// listETag returns a weak entity tag for a page of instances and its Link
// header. It covers what discovery depends on, each instance's ID, name,
// address and port, and not last_heartbeat, which changes with every
// heartbeat without changing where clients should connect: RFC 9110 calls
// such representations semantically equivalent, which is what a weak tag
// asserts.
func listETag(instances []discovery.Instance, link string) string {
	hash := sha256.New()

	for _, instance := range instances {
		hash.Write([]byte(instance.ID))
		hash.Write([]byte{0})
		hash.Write([]byte(instance.Name))
		hash.Write([]byte{0})
		hash.Write([]byte(instance.Address))
		hash.Write([]byte{0})
		hash.Write([]byte(strconv.Itoa(instance.Port)))
		hash.Write([]byte{'\n'})
	}

	hash.Write([]byte(link))

	return `W/"` + hex.EncodeToString(hash.Sum(nil)[:16]) + `"`
}

// notModified reports whether the If-None-Match header of request lists
// etag, compared weakly as RFC 9110 requires for If-None-Match, or is "*".
func notModified(request *http.Request, etag string) bool {
	for _, header := range request.Header.Values("If-None-Match") {
		for candidate := range strings.SplitSeq(header, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" || strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/") {
				return true
			}
		}
	}

	return false
}
