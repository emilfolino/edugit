package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/crewjam/saml"
)

const (
	loginCookie = "edugit_saml"
	pendingTTL  = 10 * time.Minute
	maxPending  = 10000
)

// LoginFunc is called after a successful SAML login. returnTo is a local
// path chosen when the login started.
type LoginFunc func(w http.ResponseWriter, r *http.Request, id Identity, returnTo string)

// SAMLConfig configures the service provider.
type SAMLConfig struct {
	// PublicURL is the externally visible base URL, e.g. https://git.example.edu.
	PublicURL *url.URL
	// IDPMetadata is the identity provider's metadata.
	IDPMetadata *saml.EntityDescriptor
	// Key and Cert are the SP's own keypair, see LoadOrCreateKeypair.
	Key  *rsa.PrivateKey
	Cert *x509.Certificate
	// Domains restricts logins to institutional addresses when set.
	Domains Domains
	// OnLogin receives every successfully authenticated identity.
	OnLogin LoginFunc
	Log     *slog.Logger
	// Now is overridable in tests.
	Now func() time.Time
}

// SAML is a SAML 2.0 service provider using the web-browser SSO profile.
// Logins are SP-initiated only; unsolicited (IdP-initiated) responses are
// rejected. It is safe for concurrent use. Pending requests are held in
// memory, so a restart invalidates logins in flight.
type SAML struct {
	sp  saml.ServiceProvider
	cfg SAMLConfig

	mu      sync.Mutex
	pending map[string]pending
}

type pending struct {
	returnTo string
	expires  time.Time
}

// NewSAML builds a service provider.
func NewSAML(cfg SAMLConfig) (*SAML, error) {
	if cfg.PublicURL == nil || cfg.IDPMetadata == nil || cfg.Key == nil || cfg.Cert == nil || cfg.OnLogin == nil {
		return nil, errors.New("saml: PublicURL, IDPMetadata, Key, Cert and OnLogin are required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	meta := cfg.PublicURL.JoinPath("saml", "metadata")
	acs := cfg.PublicURL.JoinPath("saml", "acs")
	return &SAML{
		cfg: cfg,
		sp: saml.ServiceProvider{
			EntityID:          meta.String(),
			Key:               cfg.Key,
			Certificate:       cfg.Cert,
			MetadataURL:       *meta,
			AcsURL:            *acs,
			IDPMetadata:       cfg.IDPMetadata,
			AuthnNameIDFormat: saml.PersistentNameIDFormat,
			AllowIDPInitiated: false,
		},
		pending: map[string]pending{},
	}, nil
}

// Routes registers the SAML endpoints on mux.
func (s *SAML) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /saml/metadata", s.metadata)
	mux.HandleFunc("GET /saml/login", s.login)
	mux.HandleFunc("POST /saml/acs", s.acs)
}

func (s *SAML) metadata(w http.ResponseWriter, _ *http.Request) {
	out, err := xml.MarshalIndent(s.sp.Metadata(), "", "  ")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml")
	_, _ = w.Write(out)
}

func (s *SAML) login(w http.ResponseWriter, r *http.Request) {
	req, err := s.sp.MakeAuthenticationRequest(
		s.sp.GetSSOBindingLocation(saml.HTTPRedirectBinding), saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		s.cfg.Log.Error("saml: make request", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	dest, err := req.Redirect("", &s.sp)
	if err != nil {
		s.cfg.Log.Error("saml: redirect", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !s.remember(req.ID, localPath(r.URL.Query().Get("next"))) {
		http.Error(w, "too many pending logins", http.StatusServiceUnavailable)
		return
	}
	// The ACS is a cross-site POST from the IdP, so the binding cookie must be
	// SameSite=None, which browsers only accept over HTTPS. Plain-http
	// deployments are for local development only.
	secure := s.cfg.PublicURL.Scheme == "https"
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteNoneMode
	}
	http.SetCookie(w, &http.Cookie{
		Name: loginCookie, Value: req.ID, Path: "/saml/acs",
		MaxAge: int(pendingTTL.Seconds()), HttpOnly: true, Secure: secure, SameSite: sameSite,
	})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, dest.String(), http.StatusFound)
}

func (s *SAML) acs(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	reqID, err := inResponseTo(r.PostForm.Get("SAMLResponse"))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// The cookie ties the response to the browser that started the login,
	// which stops an attacker from signing a victim in as themselves.
	c, err := r.Cookie(loginCookie)
	if err != nil || c.Value != reqID {
		s.cfg.Log.Warn("saml: response not bound to this browser")
		http.Error(w, "login session expired, try again", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Path: "/saml/acs", MaxAge: -1})

	returnTo, ok := s.take(reqID)
	if !ok {
		http.Error(w, "login session expired, try again", http.StatusForbidden)
		return
	}
	a, err := s.sp.ParseResponse(r, []string{reqID})
	if err != nil {
		// The detailed cause stays out of the response and the log: it may
		// echo assertion contents.
		s.cfg.Log.Warn("saml: invalid response", "kind", fmt.Sprintf("%T", err))
		http.Error(w, "authentication failed", http.StatusForbidden)
		return
	}
	id, err := identityFrom(a, s.cfg.Domains)
	if errors.Is(err, ErrDomain) {
		s.cfg.Log.Warn("saml: login refused", "reason", "email domain")
		http.Error(w, "your account is not allowed to use this service", http.StatusForbidden)
		return
	}
	if err != nil {
		s.cfg.Log.Warn("saml: bad assertion", "err", err)
		http.Error(w, "authentication failed", http.StatusForbidden)
		return
	}
	s.cfg.OnLogin(w, r, id, returnTo)
}

// remember records a pending request. It reports false when the table is full.
func (s *SAML) remember(id, returnTo string) bool {
	now := s.cfg.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, p := range s.pending {
		if now.After(p.expires) {
			delete(s.pending, k)
		}
	}
	if len(s.pending) >= maxPending {
		return false
	}
	s.pending[id] = pending{returnTo: returnTo, expires: now.Add(pendingTTL)}
	return true
}

// take consumes a pending request; each request ID is valid once, which also
// makes a replayed response useless.
func (s *SAML) take(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[id]
	delete(s.pending, id)
	if !ok || s.cfg.Now().After(p.expires) {
		return "", false
	}
	return p.returnTo, true
}

// inResponseTo extracts the request ID from a base64 SAMLResponse. It is only
// a lookup key: ParseResponse verifies the signature and that the response
// answers exactly this request.
func inResponseTo(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	var env struct {
		InResponseTo string `xml:"InResponseTo,attr"`
	}
	if err := xml.Unmarshal(raw, &env); err != nil {
		return "", err
	}
	if env.InResponseTo == "" {
		return "", errors.New("unsolicited response")
	}
	return env.InResponseTo, nil
}

// localPath returns p if it is a safe local path, else "/".
func localPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n") {
		return "/"
	}
	return p
}

// LoadIDPMetadata reads IdP metadata from an https URL or a file path.
func LoadIDPMetadata(ctx context.Context, src string) (*saml.EntityDescriptor, error) {
	var raw []byte
	if strings.HasPrefix(src, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
		if err != nil {
			return nil, err
		}
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch idp metadata: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch idp metadata: status %d", resp.StatusCode)
		}
		if raw, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20)); err != nil {
			return nil, fmt.Errorf("read idp metadata: %w", err)
		}
	} else {
		var err error
		if raw, err = os.ReadFile(src); err != nil {
			return nil, fmt.Errorf("read idp metadata: %w", err)
		}
	}
	var ed saml.EntityDescriptor
	if err := xml.Unmarshal(raw, &ed); err != nil {
		return nil, fmt.Errorf("parse idp metadata: %w", err)
	}
	if ed.EntityID == "" || len(ed.IDPSSODescriptors) == 0 {
		return nil, errors.New("idp metadata has no IDPSSODescriptor")
	}
	return &ed, nil
}

// LoadOrCreateKeypair loads the SP keypair from dir, generating a self-signed
// RSA-2048 pair (Entra ID only supports RSA) on first use.
func LoadOrCreateKeypair(dir string) (*rsa.PrivateKey, *x509.Certificate, error) {
	keyPath, crtPath := filepath.Join(dir, "saml-sp.key"), filepath.Join(dir, "saml-sp.crt")
	keyPEM, kerr := os.ReadFile(keyPath)
	crtPEM, cerr := os.ReadFile(crtPath)
	if kerr == nil && cerr == nil {
		return parseKeypair(keyPEM, crtPEM)
	}
	if !errors.Is(kerr, os.ErrNotExist) && kerr != nil {
		return nil, nil, kerr
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "edugit saml sp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER := x509.MarshalPKCS1PrivateKey(key)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER})
	crtPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(crtPath, crtPEM, 0o644); err != nil {
		return nil, nil, err
	}
	return parseKeypair(keyPEM, crtPEM)
}

func parseKeypair(keyPEM, crtPEM []byte) (*rsa.PrivateKey, *x509.Certificate, error) {
	kb, _ := pem.Decode(keyPEM)
	cb, _ := pem.Decode(crtPEM)
	if kb == nil || cb == nil {
		return nil, nil, errors.New("saml keypair: invalid PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("saml keypair: %w", err)
	}
	crt, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("saml keypair: %w", err)
	}
	return key, crt, nil
}
