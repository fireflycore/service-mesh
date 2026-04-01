package originalidentity

import (
	"strings"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
)

const (
	MetadataUserID  = "x-service-mesh-original-user-id"
	MetadataSubject = "x-service-mesh-original-user-subject"
	MetadataIssuer  = "x-service-mesh-original-user-issuer"

	SourceNone     = "none"
	SourceMetadata = "metadata"

	TrustAbsent     = "absent"
	TrustUnverified = "unverified"
)

type Identity struct {
	UserID  string
	Subject string
	Issuer  string
}

type Caller struct {
	Service   string
	Namespace string
	Env       string
}

type Effective struct {
	Identity
	Caller Caller
	Source string
	Trust  string
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

func Resolve(ctx *invokev1.InvocationContext) Effective {
	effective := Effective{
		Source: SourceNone,
		Trust:  TrustAbsent,
	}
	if ctx == nil {
		return effective
	}
	if caller := ctx.GetCaller(); caller != nil {
		effective.Caller = Caller{
			Service:   strings.TrimSpace(caller.GetService()),
			Namespace: strings.TrimSpace(caller.GetNamespace()),
			Env:       strings.TrimSpace(caller.GetEnv()),
		}
	}
	effective.Identity = Extract(ctx.GetMetadata())
	if effective.Identity.Present() {
		effective.Source = SourceMetadata
		effective.Trust = TrustUnverified
	}
	return effective
}

func (e Effective) ContextExtensions() map[string]string {
	extensions := map[string]string{
		"original_user_source": e.Source,
		"original_user_trust":  e.Trust,
	}
	if strings.TrimSpace(e.Caller.Service) != "" {
		extensions["caller_service"] = e.Caller.Service
	}
	if strings.TrimSpace(e.Caller.Namespace) != "" {
		extensions["caller_namespace"] = e.Caller.Namespace
	}
	if strings.TrimSpace(e.Caller.Env) != "" {
		extensions["caller_env"] = e.Caller.Env
	}
	if !e.Identity.Present() {
		return extensions
	}
	extensions["original_user_id"] = e.UserID
	extensions["original_user_subject"] = e.Subject
	extensions["original_user_issuer"] = e.Issuer
	return extensions
}
