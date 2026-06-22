package milkyway

import (
	"errors"
	"net/http"
	"time"
)

// Default tuning values for a Client, mirroring the reference C# SDK.
const (
	DefaultTokenRefreshSkew = 30 * time.Second
	DefaultRequestTimeout   = 30 * time.Second
	DefaultMaxRetries       = 3
	DefaultRetryBaseDelay   = 500 * time.Millisecond
)

// Options configures a Client: the API endpoint, Keycloak client-credentials, and
// resilience tuning. Construct it directly and pass to NewClient, optionally with
// functional Option overrides.
type Options struct {
	// BaseURL is the base URL of the Payments API, e.g. https://milkyway.stage.planet9.ae.
	BaseURL string

	// TokenURL is the Keycloak token endpoint for the client-credentials grant, e.g.
	// https://keycloak.ac8o.planet9.ae/realms/planet9-stage/protocol/openid-connect/token.
	TokenURL string

	// ClientID is the Keycloak client id issued to your institution (becomes the azp claim).
	ClientID string

	// ClientSecret is the Keycloak client secret issued to your institution.
	ClientSecret string

	// Scope is an optional OAuth scope to request. Empty by default.
	Scope string

	// TokenRefreshSkew is how long before a token's stated expiry it is considered
	// stale and refreshed. Guards against clock skew and in-flight latency. Default 30s.
	TokenRefreshSkew time.Duration

	// RequestTimeout is the per-attempt request timeout. Default 30s.
	RequestTimeout time.Duration

	// MaxRetries is the maximum automatic retries for transient failures
	// (5xx, 408, network). Default 3.
	MaxRetries int

	// RetryBaseDelay is the base delay for exponential backoff between retries. Default 500ms.
	RetryBaseDelay time.Duration

	// HTTPClient, when set, is used for API calls. The SDK wraps its Transport with
	// the auth + retry RoundTrippers. When nil a default client is created. The token
	// endpoint always uses a separate, un-wrapped client to avoid recursion.
	HTTPClient *http.Client
}

// Option mutates Options; pass to NewClient for ergonomic overrides.
type Option func(*Options)

// WithScope sets the OAuth scope.
func WithScope(scope string) Option { return func(o *Options) { o.Scope = scope } }

// WithMaxRetries overrides the maximum retry count.
func WithMaxRetries(n int) Option { return func(o *Options) { o.MaxRetries = n } }

// WithRequestTimeout overrides the per-attempt request timeout.
func WithRequestTimeout(d time.Duration) Option { return func(o *Options) { o.RequestTimeout = d } }

// WithRetryBaseDelay overrides the base backoff delay.
func WithRetryBaseDelay(d time.Duration) Option { return func(o *Options) { o.RetryBaseDelay = d } }

// WithTokenRefreshSkew overrides how early a cached token is refreshed before expiry.
func WithTokenRefreshSkew(d time.Duration) Option { return func(o *Options) { o.TokenRefreshSkew = d } }

// WithHTTPClient supplies a custom *http.Client for API calls.
func WithHTTPClient(c *http.Client) Option { return func(o *Options) { o.HTTPClient = c } }

func (o *Options) applyDefaults() {
	if o.TokenRefreshSkew <= 0 {
		o.TokenRefreshSkew = DefaultTokenRefreshSkew
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = DefaultRequestTimeout
	}
	if o.RetryBaseDelay <= 0 {
		o.RetryBaseDelay = DefaultRetryBaseDelay
	}
	if o.MaxRetries == 0 {
		o.MaxRetries = DefaultMaxRetries
	}
}

func (o *Options) validate() error {
	if o.BaseURL == "" {
		return errors.New("milkyway: BaseURL is required")
	}
	if o.TokenURL == "" {
		return errors.New("milkyway: TokenURL is required")
	}
	if o.ClientID == "" {
		return errors.New("milkyway: ClientID is required")
	}
	if o.ClientSecret == "" {
		return errors.New("milkyway: ClientSecret is required")
	}
	if o.MaxRetries < 0 {
		return errors.New("milkyway: MaxRetries cannot be negative")
	}
	return nil
}
