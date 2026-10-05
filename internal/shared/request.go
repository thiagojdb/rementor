package shared

import (
	"errors"
	"net"
	"net/http"
	"strconv"

	"github.com/thiagojdb/rementor/internal/access"
)

// Request identifies the exact serving record and the original Serve source.
// Receiving computers resolve SourceAddress themselves; a gateway cannot supply
// an account or device identity in its place.
type Request struct {
	GroupID       string `json:"groupId"`
	Digest        string `json:"digest"`
	SourceAddress string `json:"sourceAddress"`
	Origin        string `json:"origin"`
}

func (r Request) Validate() error {
	ip := net.ParseIP(r.SourceAddress)
	_, v4, _ := net.ParseCIDR("100.64.0.0/10")
	_, v6, _ := net.ParseCIDR("fd7a:115c:a1e0::/48")
	origin, err := access.CanonicalOrigin(r.Origin)
	if !ValidFingerprint(r.GroupID) || !ValidFingerprint(r.Digest) || ip == nil || (!v4.Contains(ip) && !v6.Contains(ip)) || err != nil || origin != r.Origin {
		return errors.New("invalid shared ingress request")
	}
	return nil
}

func SetProof(header http.Header, record Record) {
	header.Set("X-Rementor-Shared-Group", record.GroupID)
	header.Set("X-Rementor-Shared-Digest", record.Digest)
	header.Set("X-Rementor-Shared-Version", strconv.FormatUint(record.Version, 10))
	header.Set("X-Rementor-Shared-Epoch", strconv.FormatUint(record.Epoch, 10))
	header.Set("X-Rementor-Shared-Owner", record.Owner)
	header.Set("X-Rementor-Shared-Routing-Owner", record.RoutingOwner)
}

func CheckProof(header http.Header, record Record) error {
	want := make(http.Header)
	SetProof(want, record)
	for name, values := range want {
		got := header.Values(name)
		if len(got) != 1 || got[0] != values[0] {
			return ErrConflict
		}
	}
	return nil
}
