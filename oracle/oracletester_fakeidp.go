package oracle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luthersystems/lutherauth-sdk-go/jwk"
	"github.com/luthersystems/lutherauth-sdk-go/jwt"
)

// FakeIDP creates fake tokens for authentication.
type FakeIDP struct {
	fakeIDPAuthTokenPath string
	fakeIDPAuthJWKSPath  string
	key                  *jwk.Key
}

func newFakeIDP() (*FakeIDP, error) {
	return &FakeIDP{
		fakeIDPAuthTokenPath: "/test/fakeidp/token",
		fakeIDPAuthJWKSPath:  "/test/fakeidp/jwks",
		key:                  jwk.MakeTestKey(),
	}, nil
}

func (f *FakeIDP) fakeIDPAuthJWKS(next http.Handler) http.Handler {
	if f == nil {
		panic("nil fake IDP")
	}
	jwks := jwk.MakeJWKS([]*jwk.Key{f.key})
	jwksJSON, err := json.Marshal(jwks)
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != f.fakeIDPAuthJWKSPath {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.Copy(w, bytes.NewReader(jwksJSON))
	})
}

func (f *FakeIDP) fakeIDPAuthToken(next http.Handler) http.Handler {
	if f == nil {
		panic("nil fake IDP")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != f.fakeIDPAuthTokenPath {
			next.ServeHTTP(w, r)
			return
		}

		var c jwt.Claims
		decoder := json.NewDecoder(r.Body)
		err := decoder.Decode(&c)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		token, err := f.MakeFakeIDPAuthToken(&c)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tokenResp := struct {
			Token string `json:"token"`
		}{
			Token: token,
		}
		respJSON, err := json.Marshal(tokenResp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = io.Copy(w, bytes.NewReader(respJSON))
	})
}

func (f *FakeIDP) fakeIDPAuthHTTPClient(t *testing.T) (*http.Client, func()) {
	if f == nil {
		panic("nil fake IDP")
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("request to invalid route: %s", r.URL.Path)
	})
	server := httptest.NewServer(f.fakeIDPAuthToken(f.fakeIDPAuthJWKS(handler)))
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, network, _ string) (net.Conn, error) {
				return net.Dial(network, server.Listener.Addr().String())
			},
		},
	}

	return client, server.Close
}

// MakeFakeIDPAuthToken generates a token given a set of claims.
// *IMPORTANT*: only use for testing!
func (f *FakeIDP) MakeFakeIDPAuthToken(claims *jwt.Claims) (string, error) {
	if f == nil {
		return "", errors.New("nil fakeIDP")
	}
	token, err := jwk.NewJWK(f.key.PrvKey, claims, f.key.Kid)

	if err != nil {
		return "", err
	}
	return token, nil
}

// WithFakeIDP lets you fake an IDP for testing.
func (c *Config) AddFakeIDP(t *testing.T) (*FakeIDP, error) {
	if c == nil {
		return nil, errors.New("nil config")
	}
	if c.fakeIDP != nil {
		return nil, errors.New("fake IDP already configured")
	}
	f, err := newFakeIDP()
	if err != nil {
		return nil, fmt.Errorf("fake idp: %w", err)
	}
	client, stopAuthClient := f.fakeIDPAuthHTTPClient(t)
	c.AddJWKOptions(jwk.WithHTTPClient(client))
	c.stopFns = append(c.stopFns, stopAuthClient)
	c.fakeIDP = f

	return f, nil
}
