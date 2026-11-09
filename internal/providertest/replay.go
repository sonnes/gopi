package providertest

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/httprr"
)

// Server is an API that a test records from.
type Server struct {
	// KeyEnv names the environment variable that holds the key.
	KeyEnv string
	// BaseURL is the base URL of the API. Empty selects the default of
	// the SDK.
	BaseURL string
	// Fallback is the server to record from when KeyEnv is unset, or
	// nil.
	Fallback *Server
}

// Resolve returns the server that a recording goes to: s, or its
// fallback if the key of s is unset.
func (s Server) Resolve() Server {
	if os.Getenv(s.KeyEnv) == "" && s.Fallback != nil {
		return *s.Fallback
	}
	return s
}

// Replay is what a provider needs to replay the recording of a test.
type Replay struct {
	Client  *http.Client
	APIKey  string
	BaseURL string
}

// Open returns the replay of the running test. The recording is
// testdata/<test name>.httprr, with each / of the name as _. If
// -httprecord matches the recording, Open records against srv, and
// skips the test if the key of srv is unset. Else it skips a test with
// no recording.
//
// host is the host in every recording. A request that has no match in
// the recording fails the test, even if the test accepts an error from
// the provider.
func Open(t *testing.T, srv Server, host string) Replay {
	t.Helper()

	trace := filepath.Join("testdata", strings.ReplaceAll(t.Name(), "/", "_")+".httprr")
	recording, err := httprr.Recording(trace)
	require.NoError(t, err)

	r := Replay{APIKey: "fake-api-key"}
	if recording {
		srv = srv.Resolve()
		r.APIKey = os.Getenv(srv.KeyEnv)
		if r.APIKey == "" {
			t.Skipf("%s is not set", srv.KeyEnv)
		}
		r.BaseURL = srv.BaseURL
	} else if _, err := os.Stat(trace); os.IsNotExist(err) {
		t.Skipf("fixture %s not found; run make record", trace)
	}

	rr, err := httprr.Open(trace, http.DefaultTransport)
	require.NoError(t, err)
	rr.ScrubReq(scrub(host))
	t.Cleanup(func() { _ = rr.Close() })

	r.Client = rr.Client()
	if !recording {
		r.Client = &http.Client{Transport: failOnMiss{t: t, rt: rr}}
	}
	return r
}

// scrub returns a scrubber that removes the secrets and the headers
// that change between runs, so a replay matches a recording. It sets
// the host to host and cuts the path before /v1/, so a replay does not
// depend on the base URL that the recording used.
func scrub(host string) func(*http.Request) error {
	return func(req *http.Request) error {
		req.URL.Host = host
		req.Host = host
		if i := strings.Index(req.URL.Path, "/v1/"); i > 0 {
			req.URL.Path = req.URL.Path[i:]
		}
		for _, key := range []string{
			"x-api-key",
			"Authorization",
			"Accept",
			"Anthropic-Version",
			"OpenAI-Organization",
			"OpenAI-Project",
		} {
			req.Header.Del(key)
		}
		for key := range req.Header {
			if strings.HasPrefix(key, "X-Stainless-") {
				req.Header.Del(key)
			}
		}
		req.Header.Set("User-Agent", "Go-http-client/1.1")
		return nil
	}
}

// failOnMiss fails the test when rt has no response for a request.
type failOnMiss struct {
	t  *testing.T
	rt http.RoundTripper
}

func (f failOnMiss) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := f.rt.RoundTrip(req)
	if err != nil {
		f.t.Errorf("replay %s %s: %v", req.Method, req.URL, err)
	}
	return resp, err
}
