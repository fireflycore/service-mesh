package originalidentity

import (
	"strings"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
)

const (
	MetadataUserID  = "x-service-mesh-original-user-id"
	MetadataSubject = "x-service-mesh-original-user-subject"
	MetadataIssuer  = "x-service-mesh-original-user-issuer"
)

type Identity struct {
	UserID  string
	Subject string
	Issuer  string
}

func (i Identity) Present() bool {
	return strings.TrimSpace(i.UserID) != "" || strings.TrimSpace(i.Subject) != "" || strings.TrimSpace(i.Issuer) != ""
}

func Extract(entries []*invokev1.MetadataEntry) Identity {
	identity := Identity{}
	for _, entry := range entries {
		if entry == nil || len(entry.GetValues()) == 0 {
			continue
		}
		value := strings.TrimSpace(entry.GetValues()[0])
		switch strings.ToLower(strings.TrimSpace(entry.GetKey())) {
		case MetadataUserID:
			identity.UserID = value
		case MetadataSubject:
			identity.Subject = value
		case MetadataIssuer:
			identity.Issuer = value
		}
	}
	return identity
}
