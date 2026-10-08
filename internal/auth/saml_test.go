package auth

import (
	"crypto/rsa"
	"crypto/x509"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/logger"
	dsig "github.com/russellhaering/goxmldsig"
)

type fixedSession struct{ attrs []saml.Attribute }

func (f fixedSession) GetSession(_ http.ResponseWriter, _ *http.Request, _ *saml.IdpAuthnRequest) *saml.Session {
	return &saml.Session{
		ID: "s1", CreateTime: time.Now(), ExpireTime: time.Now().Add(time.Hour), Index: "i1",
		NameID: "nameid", CustomAttributes: f.attrs,
	}
}

type spLookup struct{ sp *saml.ServiceProvider }

func (l spLookup) GetServiceProvider(*http.Request, string) (*saml.EntityDescriptor, error) {
	return l.sp.Metadata(), nil
}

func attr(name, val string) saml.Attribute {
	return saml.Attribute{Name: name, Values: []saml.AttributeValue{{Type: "xs:string", Value: val}}}
}

func entraAttrs(oid, email string) []saml.Attribute {
	return []saml.Attribute{attr(attrObjectID, oid), attr(attrEmail, email), attr(attrDisplayName, "Test User")}
}

type env struct {
	sp    *SAML
	idp   *saml.IdentityProvider
	got   []Identity
	paths []string
}

// newEnv wires an SP to an in-process IdP. idpDir holds the IdP's keypair; a
// different directory yields an IdP whose signatures the SP must reject.
func newEnv(t *testing.T, signerDir string, attrs []saml.Attribute, domains Domains) *env {
	t.Helper()
	e := &env{}
	pub, _ := url.Parse("http://sp.test")
	idpKey, idpCrt, err := LoadOrCreateKeypair(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	signKey, signCrt := idpKey, idpCrt
	if signerDir != "" {
		if signKey, signCrt, err = LoadOrCreateKeypair(signerDir); err != nil {
			t.Fatal(err)
		}
	}
	ssoURL, _ := url.Parse("http://idp.test/sso")
	metaURL, _ := url.Parse("http://idp.test/metadata")
	e.idp = &saml.IdentityProvider{
		Key: signKey, Signer: signKey, Certificate: signCrt,
		MetadataURL: *metaURL, SSOURL: *ssoURL,
		SessionProvider: fixedSession{attrs},
		Logger:          logger.DefaultLogger,
		SignatureMethod: dsig.RSASHA256SignatureMethod,
	}
	// The SP trusts idpCrt; signKey may differ.
	trusted := (&saml.IdentityProvider{Key: idpKey, Certificate: idpCrt, MetadataURL: *metaURL, SSOURL: *ssoURL}).Metadata()

	key, crt, err := LoadOrCreateKeypair(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.sp, err = NewSAML(SAMLConfig{
		PublicURL: pub, IDPMetadata: trusted, Key: key, Cert: crt, Domains: domains,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnLogin: func(w http.ResponseWriter, _ *http.Request, id Identity, returnTo string) {
			e.got = append(e.got, id)
			e.paths = append(e.paths, returnTo)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.idp.ServiceProviderProvider = spLookup{&e.sp.sp}
	return e
}

var formRE = regexp.MustCompile(`name="SAMLResponse" value="([^"]*)"`)

// login performs the browser leg: /saml/login, the IdP, and returns the
// SAMLResponse and the login cookie.
func (e *env) login(t *testing.T, next string) (resp string, cookie *http.Cookie) {
	t.Helper()
	mux := http.NewServeMux()
	e.sp.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/saml/login?next="+url.QueryEscape(next), nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login: got status %d, want 302", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == loginCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("login: no cookie set")
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	idpRec := httptest.NewRecorder()
	e.idp.ServeSSO(idpRec, httptest.NewRequest("GET", "/sso?"+loc.RawQuery, nil))
	m := formRE.FindStringSubmatch(idpRec.Body.String())
	if m == nil {
		t.Fatalf("idp returned no SAMLResponse: %d %s", idpRec.Code, idpRec.Body)
	}
	return html.UnescapeString(m[1]), cookie
}

func (e *env) acs(resp string, cookie *http.Cookie) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	e.sp.Routes(mux)
	req := httptest.NewRequest("POST", "/saml/acs", strings.NewReader(url.Values{"SAMLResponse": {resp}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestSAML_login(t *testing.T) {
	const oid = "11111111-2222-3333-4444-555555555555"
	dom := Domains{Staff: "bth.se", Student: "student.bth.se"}

	t.Run("valid", func(t *testing.T) {
		e := newEnv(t, "", entraAttrs(oid, "EFO@bth.se"), dom)
		resp, c := e.login(t, "/courses")
		if rec := e.acs(resp, c); rec.Code != http.StatusNoContent {
			t.Fatalf("acs: got %d %s, want 204", rec.Code, rec.Body)
		}
		want := Identity{Subject: oid, Email: "efo@bth.se", DisplayName: "Test User"}
		if len(e.got) != 1 || e.got[0] != want {
			t.Errorf("identity: got %+v, want %+v", e.got, want)
		}
		if e.paths[0] != "/courses" {
			t.Errorf("returnTo: got %q, want /courses", e.paths[0])
		}
	})

	t.Run("open redirect target is neutralised", func(t *testing.T) {
		e := newEnv(t, "", entraAttrs(oid, "a@bth.se"), dom)
		resp, c := e.login(t, "//evil.example")
		e.acs(resp, c)
		if e.paths[0] != "/" {
			t.Errorf("returnTo: got %q, want /", e.paths[0])
		}
	})

	t.Run("replay", func(t *testing.T) {
		e := newEnv(t, "", entraAttrs(oid, "a@bth.se"), dom)
		resp, c := e.login(t, "/")
		e.acs(resp, c)
		if rec := e.acs(resp, c); rec.Code != http.StatusForbidden {
			t.Errorf("replay: got %d, want 403", rec.Code)
		}
		if len(e.got) != 1 {
			t.Errorf("logins: got %d, want 1", len(e.got))
		}
	})

	t.Run("no binding cookie", func(t *testing.T) {
		e := newEnv(t, "", entraAttrs(oid, "a@bth.se"), dom)
		resp, _ := e.login(t, "/")
		if rec := e.acs(resp, nil); rec.Code != http.StatusForbidden {
			t.Errorf("got %d, want 403", rec.Code)
		}
	})

	t.Run("wrong signer", func(t *testing.T) {
		e := newEnv(t, t.TempDir(), entraAttrs(oid, "a@bth.se"), dom)
		resp, c := e.login(t, "/")
		if rec := e.acs(resp, c); rec.Code != http.StatusForbidden || len(e.got) != 0 {
			t.Errorf("got %d with %d logins, want 403 and none", rec.Code, len(e.got))
		}
	})

	t.Run("expired assertion", func(t *testing.T) {
		e := newEnv(t, "", entraAttrs(oid, "a@bth.se"), dom)
		resp, c := e.login(t, "/")
		orig := saml.TimeNow
		saml.TimeNow = func() time.Time { return orig().Add(time.Hour) }
		t.Cleanup(func() { saml.TimeNow = orig })
		if rec := e.acs(resp, c); rec.Code != http.StatusForbidden || len(e.got) != 0 {
			t.Errorf("got %d with %d logins, want 403 and none", rec.Code, len(e.got))
		}
	})

	t.Run("unsolicited", func(t *testing.T) {
		e := newEnv(t, "", entraAttrs(oid, "a@bth.se"), dom)
		_, c := e.login(t, "/")
		if rec := e.acs("bm90IHNhbWw=", c); rec.Code != http.StatusBadRequest {
			t.Errorf("got %d, want 400", rec.Code)
		}
	})

	t.Run("foreign domain", func(t *testing.T) {
		e := newEnv(t, "", entraAttrs(oid, "x@gmail.com"), dom)
		resp, c := e.login(t, "/")
		if rec := e.acs(resp, c); rec.Code != http.StatusForbidden || len(e.got) != 0 {
			t.Errorf("got %d with %d logins, want 403 and none", rec.Code, len(e.got))
		}
	})

	t.Run("missing object id", func(t *testing.T) {
		e := newEnv(t, "", []saml.Attribute{attr(attrEmail, "a@bth.se")}, dom)
		resp, c := e.login(t, "/")
		if rec := e.acs(resp, c); rec.Code != http.StatusForbidden || len(e.got) != 0 {
			t.Errorf("got %d with %d logins, want 403 and none", rec.Code, len(e.got))
		}
	})
}

func TestSAML_metadata(t *testing.T) {
	e := newEnv(t, "", nil, Domains{})
	mux := http.NewServeMux()
	e.sp.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/saml/metadata", nil))
	body := rec.Body.String()
	for _, want := range []string{"http://sp.test/saml/metadata", "http://sp.test/saml/acs"} {
		if !strings.Contains(body, want) {
			t.Errorf("metadata lacks %q", want)
		}
	}
}

func TestDomains_Kind(t *testing.T) {
	d := Domains{Staff: "bth.se", Student: "student.bth.se"}
	tests := []struct {
		email string
		want  Kind
	}{
		{"a@bth.se", KindStaff},
		{"A@BTH.SE", KindStaff},
		{"a@student.bth.se", KindStudent},
		{"a@evil.bth.se", KindOther},
		{"a@bth.se.evil.com", KindOther},
		{"a@xbth.se", KindOther},
		{"bth.se", KindOther},
		{"x@bth.se@evil.com", KindOther},
		{"", KindOther},
	}
	for _, tt := range tests {
		if got := d.Kind(tt.email); got != tt.want {
			t.Errorf("Kind(%q): got %v, want %v", tt.email, got, tt.want)
		}
	}
}

func TestLoadOrCreateKeypair_persists(t *testing.T) {
	dir := t.TempDir()
	k1, c1, err := LoadOrCreateKeypair(dir)
	if err != nil {
		t.Fatal(err)
	}
	k2, c2, err := LoadOrCreateKeypair(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !k1.Equal(k2) || !c1.Equal(c2) {
		t.Error("second load returned a different keypair")
	}
	var _ *rsa.PrivateKey = k1
	var _ *x509.Certificate = c1
}
