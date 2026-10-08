// Package auth authenticates users through SAML single sign-on. The identity
// provider only proves who someone is; roles and permissions live in edugit
// (see TODO #7), so nothing here grants access.
package auth

import (
	"errors"
	"fmt"
	"strings"

	"github.com/crewjam/saml"
)

// Attribute names sent by Microsoft Entra ID.
const (
	attrObjectID    = "http://schemas.microsoft.com/identity/claims/objectidentifier"
	attrEmail       = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress"
	attrUPN         = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name"
	attrDisplayName = "http://schemas.microsoft.com/identity/claims/displayname"
)

// ErrDomain is returned when the asserted email is outside the configured
// institutional domains.
var ErrDomain = errors.New("email domain not allowed")

// Identity is the authenticated person as asserted by the identity provider.
type Identity struct {
	// Subject is the stable IdP identifier (Entra object ID). Emails and
	// UPNs change; this does not.
	Subject     string
	Email       string
	DisplayName string
}

// Kind is the institutional category implied by an email domain. It is an
// eligibility hint only, never a grant of a role.
type Kind int

// Kinds of institutional account.
const (
	KindOther Kind = iota
	KindStaff
	KindStudent
)

// Domains classifies emails by exact domain match. A suffix match would
// wrongly treat student.example.edu as a subdomain of staff example.edu.
type Domains struct {
	Staff   string
	Student string
}

// Kind classifies email. The comparison is case-insensitive and exact on the
// part after the last "@".
func (d Domains) Kind(email string) Kind {
	i := strings.LastIndexByte(email, '@')
	if i < 0 {
		return KindOther
	}
	dom := strings.ToLower(email[i+1:])
	switch {
	case d.Staff != "" && dom == strings.ToLower(d.Staff):
		return KindStaff
	case d.Student != "" && dom == strings.ToLower(d.Student):
		return KindStudent
	}
	return KindOther
}

// restricted reports whether any domain is configured.
func (d Domains) restricted() bool { return d.Staff != "" || d.Student != "" }

// identityFrom extracts an Identity from a validated assertion. The subject
// claim is required.
func identityFrom(a *saml.Assertion, d Domains) (Identity, error) {
	vals := map[string]string{}
	for _, st := range a.AttributeStatements {
		for _, at := range st.Attributes {
			if len(at.Values) > 0 {
				vals[at.Name] = strings.TrimSpace(at.Values[0].Value)
			}
		}
	}
	id := Identity{
		Subject:     vals[attrObjectID],
		Email:       vals[attrEmail],
		DisplayName: vals[attrDisplayName],
	}
	if id.Subject == "" {
		return Identity{}, fmt.Errorf("assertion lacks %s claim", attrObjectID)
	}
	if id.Email == "" && strings.Contains(vals[attrUPN], "@") {
		id.Email = vals[attrUPN]
	}
	id.Email = strings.ToLower(id.Email)
	if id.DisplayName == "" {
		id.DisplayName = id.Email
	}
	if d.restricted() && d.Kind(id.Email) == KindOther {
		return Identity{}, ErrDomain
	}
	return id, nil
}
