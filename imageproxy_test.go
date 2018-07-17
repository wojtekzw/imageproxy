// Copyright 2013 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package imageproxy

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/wojtekzw/imageproxy/ip"
)

func TestCopyHeader(t *testing.T) {
	tests := []struct {
		dst, src http.Header
		keys     []string
		want     http.Header
	}{
		// empty
		{http.Header{}, http.Header{}, nil, http.Header{}},
		{http.Header{}, http.Header{}, []string{}, http.Header{}},
		{http.Header{}, http.Header{}, []string{"A"}, http.Header{}},

		// nothing to copy
		{
			dst:  http.Header{"A": []string{"a1"}},
			src:  http.Header{},
			keys: nil,
			want: http.Header{"A": []string{"a1"}},
		},
		{
			dst:  http.Header{},
			src:  http.Header{"A": []string{"a"}},
			keys: []string{"B"},
			want: http.Header{},
		},

		// copy headers
		{
			dst:  http.Header{},
			src:  http.Header{"A": []string{"a"}},
			keys: nil,
			want: http.Header{"A": []string{"a"}},
		},
		{
			dst:  http.Header{"A": []string{"a"}},
			src:  http.Header{"B": []string{"b"}},
			keys: nil,
			want: http.Header{"A": []string{"a"}, "B": []string{"b"}},
		},
		{
			dst:  http.Header{"A": []string{"a"}},
			src:  http.Header{"B": []string{"b"}, "C": []string{"c"}},
			keys: []string{"B"},
			want: http.Header{"A": []string{"a"}, "B": []string{"b"}},
		},
		{
			dst:  http.Header{"A": []string{"a1"}},
			src:  http.Header{"A": []string{"a2"}},
			keys: nil,
			want: http.Header{"A": []string{"a1", "a2"}},
		},
	}

	for _, tt := range tests {
		// copy dst map
		got := make(http.Header)
		for k, v := range tt.dst {
			got[k] = v
		}

		copyHeader(got, tt.src, tt.keys...)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("copyHeader(%v, %v, %v) returned %v, want %v", tt.dst, tt.src, tt.keys, got, tt.want)
		}

	}
}

func TestAllowed(t *testing.T) {
	whitelist := []string{"good", "good.ref", "good.nosig"}
	key := []byte("c0ffee")

	genRequest := func(headers map[string]string) *http.Request {
		req := &http.Request{Header: make(http.Header)}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		return req
	}

	cnameTest := func(s string) (string, error) {
		switch s {
		default:
			return "", fmt.Errorf("no cname record")
		}
	}

	tests := []struct {
		url       string
		options   Options
		whitelist []string
		referrers []string
		key       []byte
		request   *http.Request
		allowed   bool
	}{
		// no whitelist or signature key
		{"http://test/image", emptyOptions, nil, nil, nil, nil, true},

		// whitelist
		{"http://good/image", emptyOptions, whitelist, nil, nil, nil, true},
		{"http://bad/image", emptyOptions, whitelist, nil, nil, nil, false},

		// referrer
		{"http://test/image", emptyOptions, nil, whitelist, nil, genRequest(map[string]string{"Referer": "http://good.ref/foo"}), true},
		{"http://test/image", emptyOptions, nil, whitelist, nil, genRequest(map[string]string{"Referer": "http://bad.ref/foo"}), false},
		{"http://test/image", emptyOptions, nil, whitelist, nil, genRequest(map[string]string{"Referer": "MALFORMED!!"}), false},
		{"http://test/image", emptyOptions, nil, whitelist, nil, genRequest(map[string]string{}), false},

		// signature key
		{"http://test/image", Options{Signature: "NDx5zZHx7QfE8E-ijowRreq6CJJBZjwiRfOVk_mkfQQ="}, nil, nil, key, nil, true},
		{"http://test/image", Options{Signature: "deadbeef"}, nil, nil, key, nil, false},
		{"http://test/image", emptyOptions, nil, nil, key, nil, false},

		// whitelist and signature
		{"http://good.nosig/image", emptyOptions, whitelist, nil, key, nil, true},
		{"http://bad/image", Options{Signature: "gWivrPhXBbsYEwpmWAKjbJEiAEgZwbXbltg95O2tgNI="}, nil, nil, key, nil, true},
		{"http://bad.nosig/image", emptyOptions, whitelist, nil, key, nil, false},
	}

	for _, tt := range tests {
		p := NewProxy(nil, nil, MaxRespBodySize)
		p.Whitelist = tt.whitelist
		p.SignatureKey = tt.key
		p.Referrers = tt.referrers

		u, err := url.Parse(tt.url)
		if err != nil {
			t.Errorf("error parsing url %q: %v", tt.url, err)
		}
		req := &Request{u, tt.options, tt.request}
		if got, want := p.allowed(req, cnameTest), tt.allowed; (got == nil) != want {
			t.Errorf("allowed(%q) returned %v, want %v.\nTest struct: %#v", req, got, want, tt)
		}
	}
}

func testAllowedCached(id int, t *testing.T, wl []string, wlIP []ip.Range, sigKey []byte, url string, allowed bool) {

	p := NewProxy(nil, nil, 0)
	p.Whitelist = wl
	p.WhitelistIP = wlIP
	p.SignatureKey = sigKey

	reqHTTP, err := http.NewRequest("GET", url, strings.NewReader("request body"))
	if err != nil {
		t.Fatal(err)
	}
	req, err := NewRequest(reqHTTP, nil)
	if err != nil {
		t.Fatal(err)
	}

	allowedHosts.Purge()
	notAllowedHosts.Purge()

	// first check to fill in cache
	err = p.allowedCached(req, cname)
	if allowed && err != nil || !allowed && err == nil {
		t.Fatalf("id: %d, first check: expected allowed: %t, got allowed: %t (%v)", id, allowed, err == nil, err)
	}

	// second check from cache
	err = p.allowedCached(req, cname)
	if allowed && err != nil || !allowed && err == nil {
		t.Fatalf("id: %d, second check: expected allowed: %t, got allowed: %t (%v)", id, allowed, err == nil, err)
	}

}

func TestAllowedCached(t *testing.T) {
	whitelistIPPositive := []ip.Range{
		{
			From: net.ParseIP("216.58.0.0"),
			To:   net.ParseIP("216.58.255.255"),
		},
		{
			From: net.ParseIP("172.217.0.0"),
			To:   net.ParseIP("172.217.255.255"),
		},
		{
			From: net.ParseIP("127.0.0.1"),
			To:   net.ParseIP("127.0.0.1"),
		},
		{
			From: net.ParseIP("::1"),
			To:   net.ParseIP("::1"),
		},
	}

	whitelistIPNegative := []ip.Range{
		{
			From: net.ParseIP("192.168.22.0"),
			To:   net.ParseIP("192.168.22.255"),
		},
		{
			From: net.ParseIP("192.217.0.0"),
			To:   net.ParseIP("192.217.255.255"),
		},
	}

	whitelistPositive := []string{"example.com", "www.google.com", "*.google.com", "google.com", "localhost:81", "127.0.0.1:81"}
	whitelistNegative := []string{"example.com", "localhost:90", "localhost", "127.0.0.1"}

	nonEmptySigKey := []byte("12344556")

	testAllowedCached(1, t, nil, nil, nil, "http://localhost/x114/http://google.com/a.jpg", true)
	testAllowedCached(2, t, whitelistPositive, nil, nil, "http://localhost/x114/http://google.com/a.jpg", true)
	testAllowedCached(3, t, whitelistNegative, nil, nil, "http://localhost/x114/http://google.com/a.jpg", false)
	testAllowedCached(4, t, nil, whitelistIPPositive, nil, "http://localhost/x114/http://google.com/a.jpg", true)
	testAllowedCached(5, t, nil, whitelistIPNegative, nil, "http://localhost/x114/http://google.com/a.jpg", false)

	testAllowedCached(21, t, nil, nil, nonEmptySigKey, "http://localhost/x114/http://google.com/a.jpg", false)
	testAllowedCached(22, t, whitelistPositive, nil, nonEmptySigKey, "http://localhost/x114/http://google.com/a.jpg", true)
	testAllowedCached(23, t, whitelistNegative, nil, nonEmptySigKey, "http://localhost/x114/http://google.com/a.jpg", false)
	testAllowedCached(24, t, nil, whitelistIPPositive, nonEmptySigKey, "http://localhost/x114/http://google.com/a.jpg", true)
	testAllowedCached(25, t, nil, whitelistIPNegative, nonEmptySigKey, "http://localhost/x114/http://google.com/a.jpg", false)

	testAllowedCached(31, t, nil, nil, nil, "http://localhost/x114,s09876554/http://google.com/a.jpg", true)
	testAllowedCached(32, t, whitelistPositive, nil, nil, "http://localhost/x114,s09876554/http://google.com/a.jpg", true)
	testAllowedCached(33, t, whitelistNegative, nil, nil, "http://localhost/x114,s09876554/http://google.com/a.jpg", false)
	testAllowedCached(34, t, nil, whitelistIPPositive, nil, "http://localhost/x114,s09876554/http://google.com/a.jpg", true)
	testAllowedCached(35, t, nil, whitelistIPNegative, nil, "http://localhost/x114,s09876554/http://google.com/a.jpg", false)

	testAllowedCached(41, t, nil, nil, nonEmptySigKey, "http://localhost/x114,s09876554/http://google.com/a.jpg", false)
	testAllowedCached(42, t, whitelistPositive, nil, nonEmptySigKey, "http://localhost/x114,s09876554/http://google.com/a.jpg", true)
	testAllowedCached(43, t, whitelistNegative, nil, nonEmptySigKey, "http://localhost/x114,s09876554/http://google.com/a.jpg", false)
	testAllowedCached(44, t, nil, whitelistIPPositive, nonEmptySigKey, "http://localhost/x114,s09876554/http://google.com/a.jpg", true)
	testAllowedCached(45, t, nil, whitelistIPNegative, nonEmptySigKey, "http://localhost/x114,s09876554/http://google.com/a.jpg", false)

	testAllowedCached(51, t, nil, nil, nonEmptySigKey, "http://localhost/x114,s09876554/http://localhost:81/a.jpg", false)
	testAllowedCached(52, t, whitelistPositive, nil, nonEmptySigKey, "http://localhost/x114,s09876554/http://localhost:81/a.jpg", true)
	testAllowedCached(53, t, whitelistPositive, nil, nonEmptySigKey, "http://localhost/x114,s09876554/http://127.0.0.1:81/a.jpg", true)
	testAllowedCached(54, t, whitelistNegative, nil, nonEmptySigKey, "http://localhost/x114,s09876554/http://localhost:81/a.jpg", false)
	testAllowedCached(55, t, whitelistNegative, nil, nonEmptySigKey, "http://localhost/x114,s09876554/http://127.0.0.1:81/a.jpg", false)
	testAllowedCached(56, t, nil, whitelistIPPositive, nonEmptySigKey, "http://localhost/x114,s09876554/http://127.0.0.1:81/a.jpg", true)
	testAllowedCached(57, t, whitelistNegative, whitelistIPPositive, nonEmptySigKey, "http://localhost/x114,s09876554/http://127.0.0.1:81/a.jpg", true)
	testAllowedCached(58, t, whitelistNegative, whitelistIPPositive, nonEmptySigKey, "http://localhost/x114,s09876554/http://localhost:81/a.jpg", true)
	testAllowedCached(59, t, whitelistNegative, whitelistIPPositive, nonEmptySigKey, "http://localhost/x114,s09876554/http://localhost/a.jpg", true)
	testAllowedCached(60, t, nil, whitelistIPNegative, nonEmptySigKey, "http://localhost/x114,s09876554/http://localhost:81/a.jpg", false)

}

func TestValidHost(t *testing.T) {
	whitelist := []string{"a.test", "a.test:81", "*.b.test", "*c.test"}

	tests := []struct {
		url   string
		valid bool
	}{
		{"http://a.test/image", true},
		{"http://x.a.test/image", false},

		{"http://b.test/image", true},
		{"http://x.b.test/image", true},
		{"http://x.y.b.test/image", true},

		{"http://c.test/image", false},
		{"http://xc.test/image", false},
		{"/image", false},

		{"http://d.test/image", true},
		{"http://d.test:81/image", true},
		{"http://d.test:90/image", false},
		{"http://e.test/image", true},
		{"http://a.f.test/image", false},
		{"http://b.f.test/image", false},
		{"http://xxx.test/image", false},
	}

	cnameTest := func(s string) (string, error) {
		switch s {
		case "d.test":
			return "a.test", nil
		case "e.test":
			return "b.test", nil
		case "a.f.test":
			return "", nil
		case "b.f.test":
			return "xxx", fmt.Errorf("invalid cname query")
		default:
			return "", fmt.Errorf("no cname record")
		}
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.url)
		if err != nil {
			t.Errorf("error parsing url %q: %v", tt.url, err)
		}
		if got, want := validHostWithCNAME(whitelist, u, cnameTest), tt.valid; got != want {
			t.Errorf("validHostWithCNAME(%v, %q) returned %v, want %v", whitelist, u, got, want)
		}
	}
}

func TestValidSignature(t *testing.T) {
	key := []byte("c0ffee")

	tests := []struct {
		url     string
		options Options
		valid   bool
	}{
		{"http://test/image", Options{Signature: "NDx5zZHx7QfE8E-ijowRreq6CJJBZjwiRfOVk_mkfQQ="}, true},
		{"http://test/image", Options{Signature: "NDx5zZHx7QfE8E-ijowRreq6CJJBZjwiRfOVk_mkfQQ"}, true},
		{"http://test/image", emptyOptions, false},
	}

	for _, tt := range tests {
		u, err := url.Parse(tt.url)
		if err != nil {
			t.Errorf("error parsing url %q: %v", tt.url, err)
		}
		req := &Request{u, tt.options, &http.Request{}}
		if got, want := validSignature(key, req), tt.valid; got != want {
			t.Errorf("validSignature(%v, %q) returned %v, want %v", key, u, got, want)
		}
	}
}

func TestShould304(t *testing.T) {
	tests := []struct {
		req, resp string
		is304     bool
	}{
		{ // etag match
			"GET / HTTP/1.1\nIf-None-Match: \"v\"\n\n",
			"HTTP/1.1 200 OK\nEtag: \"v\"\n\n",
			true,
		},
		{ // last-modified match
			"GET / HTTP/1.1\nIf-Modified-Since: Sun, 02 Jan 2000 00:00:00 GMT\n\n",
			"HTTP/1.1 200 OK\nLast-Modified: Sat, 01 Jan 2000 00:00:00 GMT\n\n",
			true,
		},

		// mismatches
		{
			"GET / HTTP/1.1\n\n",
			"HTTP/1.1 200 OK\n\n",
			false,
		},
		{
			"GET / HTTP/1.1\n\n",
			"HTTP/1.1 200 OK\nEtag: \"v\"\n\n",
			false,
		},
		{
			"GET / HTTP/1.1\nIf-None-Match: \"v\"\n\n",
			"HTTP/1.1 200 OK\n\n",
			false,
		},
		{
			"GET / HTTP/1.1\nIf-None-Match: \"a\"\n\n",
			"HTTP/1.1 200 OK\nEtag: \"b\"\n\n",
			false,
		},
		{ // last-modified match
			"GET / HTTP/1.1\n\n",
			"HTTP/1.1 200 OK\nLast-Modified: Sat, 01 Jan 2000 00:00:00 GMT\n\n",
			false,
		},
		{ // last-modified match
			"GET / HTTP/1.1\nIf-Modified-Since: Sun, 02 Jan 2000 00:00:00 GMT\n\n",
			"HTTP/1.1 200 OK\n\n",
			false,
		},
		{ // last-modified match
			"GET / HTTP/1.1\nIf-Modified-Since: Fri, 31 Dec 1999 00:00:00 GMT\n\n",
			"HTTP/1.1 200 OK\nLast-Modified: Sat, 01 Jan 2000 00:00:00 GMT\n\n",
			false,
		},
	}

	for _, tt := range tests {
		buf := bufio.NewReader(strings.NewReader(tt.req))
		req, err := http.ReadRequest(buf)
		if err != nil {
			t.Errorf("http.ReadRequest(%q) returned error: %v", tt.req, err)
		}

		buf = bufio.NewReader(strings.NewReader(tt.resp))
		resp, err := http.ReadResponse(buf, req)
		if err != nil {
			t.Errorf("http.ReadResponse(%q) returned error: %v", tt.resp, err)
		}

		if got, want := should304(req, resp), tt.is304; got != want {
			t.Errorf("should304(%q, %q) returned: %v, want %v", tt.req, tt.resp, got, want)
		}
	}
}

// testTransport is an http.RoundTripper that returns certained canned
// responses for particular requests.
type testTransport struct{}

func (t testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var raw string

	switch req.URL.Path {
	case "/ok":
		raw = "HTTP/1.1 200 OK\n\n"
	case "/error":
		return nil, errors.New("http protocol error")
	case "/nocontent":
		raw = "HTTP/1.1 204 No Content\n\n"
	case "/etag":
		raw = "HTTP/1.1 200 OK\nEtag: \"tag\"\n\n"
	case "/png":
		m := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		img := new(bytes.Buffer)
		png.Encode(img, m)

		raw = fmt.Sprintf("HTTP/1.1 200 OK\nContent-Length: %d\n\n%s", len(img.Bytes()), img.Bytes())
	default:
		raw = "HTTP/1.1 404 Not Found\n\n"
	}

	buf := bufio.NewReader(bytes.NewBufferString(raw))
	return http.ReadResponse(buf, req)
}

func TestProxy_ServeHTTP(t *testing.T) {
	p := &Proxy{
		Client: &http.Client{
			Transport: testTransport{},
		},
		Whitelist: []string{"good.test"},
	}

	tests := []struct {
		url  string // request URL
		code int    // expected response status code
	}{
		{"/favicon.ico", http.StatusOK},
		{"//foo", http.StatusBadRequest},                            // invalid request URL
		{"/http://bad.test/", http.StatusForbidden},                 // Disallowed host
		{"/http://good.test/error", http.StatusInternalServerError}, // HTTP protocol error
		{"/http://good.test/nocontent", http.StatusNoContent},       // non-OK response

		{"/100/http://good.test/ok", http.StatusOK},
	}

	for _, tt := range tests {
		req, _ := http.NewRequest("GET", "http://localhost"+tt.url, nil)
		resp := httptest.NewRecorder()
		p.ServeHTTP(resp, req)

		if got, want := resp.Code, tt.code; got != want {
			t.Errorf("ServeHTTP(%v) returned status %d, want %d", req, got, want)
		}
	}
}

// test that 304 Not Modified responses are returned properly.
func TestProxy_ServeHTTP_is304(t *testing.T) {
	p := &Proxy{
		Client: &http.Client{
			Transport: testTransport{},
		},
	}

	req, _ := http.NewRequest("GET", "http://localhost/http://good.test/etag", nil)
	req.Header.Add("If-None-Match", `"tag"`)
	resp := httptest.NewRecorder()
	p.ServeHTTP(resp, req)

	if got, want := resp.Code, http.StatusNotModified; got != want {
		t.Errorf("ServeHTTP(%v) returned status %d, want %d", req, got, want)
	}
	if got, want := resp.Header().Get("Etag"), `"tag"`; got != want {
		t.Errorf("ServeHTTP(%v) returned etag header %v, want %v", req, got, want)
	}
}

func TestTransformingTransport(t *testing.T) {
	client := new(http.Client)
	tr := &TransformingTransport{
		Transport:       testTransport{},
		CachingClient:   client,
		MaxResponseSize: MaxRespBodySize,
	}
	// TODO: test MaxResponseSize works as designed

	client.Transport = tr

	tests := []struct {
		url         string
		code        int
		expectError bool
	}{
		//{"http://good.test/png#1", http.StatusOK, false},
		{"http://good.test/error#1", http.StatusInternalServerError, true},
		// TODO: test more than just status code... verify that image
		// is actually transformed and returned properly and that
		// non-image responses are returned as-is
	}

	for _, tt := range tests {
		req, _ := http.NewRequest("GET", tt.url, nil)

		resp, err := tr.RoundTrip(req)
		if err != nil {
			if !tt.expectError {
				t.Errorf("RoundTrip(%v) returned unexpected error: %v", tt.url, err)
			}
			continue
		} else if tt.expectError {
			t.Errorf("RoundTrip(%v) did not return expected error", tt.url)
		}
		if got, want := resp.StatusCode, tt.code; got != want {
			t.Errorf("RoundTrip(%v) returned status code %d, want %d", tt.url, got, want)
		}
	}
}

func TestValidIP(t *testing.T) {
	whitelist := []ip.Range{
		{
			From: net.ParseIP("192.168.22.0"),
			To:   net.ParseIP("192.168.22.255"),
		},
		{
			From: net.ParseIP("192.0.79.32"),
			To:   net.ParseIP("192.0.79.35"),
		},
		{
			From: net.ParseIP("173.194.220.100"),
			To:   net.ParseIP("173.194.220.255"),
		},
	}
	if !validIPBool(whitelist, "nypost.com") {
		t.Error("validIP (NYPost) - expected: 'true', got: 'false'")
	}
	if validIPBool(whitelist, "yahoo.com") {
		t.Error("validIP (Yahoo) - expected: 'false, got: 'true'")
	}
}

func BenchmarkAllowedIP(b *testing.B) {
	whitelistIP := []ip.Range{
		{
			From: net.ParseIP("216.58.0.0"),
			To:   net.ParseIP("216.58.255.255"),
		},
		{
			From: net.ParseIP("172.217.0.0"),
			To:   net.ParseIP("172.217.255.255"),
		},
		{
			From: net.ParseIP("173.194.220.100"),
			To:   net.ParseIP("173.194.220.255"),
		},
	}

	p := NewProxy(nil, nil, 0)
	p.WhitelistIP = whitelistIP

	reqHTTP, err := http.NewRequest("GET", "http://localhost/x114/http://google.com/a.jpg", strings.NewReader("request body"))
	if err != nil {
		b.Fatal(err)
	}
	req, err := NewRequest(reqHTTP, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		err := p.allowed(req, cname)
		if err != nil {
			b.Fatal("expected allowed result")
		}

	}
}

func BenchmarkAllowedIPCachedPositive(b *testing.B) {
	whitelistIP := []ip.Range{
		{
			From: net.ParseIP("216.58.0.0"),
			To:   net.ParseIP("216.58.255.255"),
		},
		{
			From: net.ParseIP("172.217.0.0"),
			To:   net.ParseIP("172.217.255.255"),
		},
		{
			From: net.ParseIP("173.194.220.100"),
			To:   net.ParseIP("173.194.220.255"),
		},
	}

	p := NewProxy(nil, nil, 0)
	p.WhitelistIP = whitelistIP

	reqHTTP, err := http.NewRequest("GET", "http://localhost/x114/http://google.com/a.jpg", strings.NewReader("request body"))
	if err != nil {
		b.Fatal(err)
	}
	req, err := NewRequest(reqHTTP, nil)
	if err != nil {
		b.Fatal(err)
	}

	allowedHosts.Purge()
	notAllowedHosts.Purge()

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		err := p.allowedCached(req, cname)
		if err != nil {
			b.Fatal("expected allowed result")
		}

	}
}

func BenchmarkAllowedIPCachedNegativeWithSignature(b *testing.B) {
	whitelistIP := []ip.Range{
		{
			From: net.ParseIP("192.168.22.0"),
			To:   net.ParseIP("192.168.22.255"),
		},
	}

	p := NewProxy(nil, nil, 0)
	p.WhitelistIP = whitelistIP
	p.SignatureKey = []byte("12334445556")

	reqHTTP, err := http.NewRequest("GET", "http://localhost/x114/http://google.com/a.jpg", strings.NewReader("request body"))
	if err != nil {
		b.Fatal(err)
	}
	req, err := NewRequest(reqHTTP, nil)
	if err != nil {
		b.Fatal(err)
	}

	allowedHosts.Purge()
	notAllowedHosts.Purge()

	// fill-in cache
	p.allowedCached(req, cname)

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		err := p.allowedCached(req, cname)
		if err == nil {
			b.Fatal("expected not allowed result")
		}
	}
}

func BenchmarkAllowedIPCachedNegativeNoSignature(b *testing.B) {
	whitelistIP := []ip.Range{
		{
			From: net.ParseIP("192.168.22.0"),
			To:   net.ParseIP("192.168.22.255"),
		},
	}

	p := NewProxy(nil, nil, 0)
	p.WhitelistIP = whitelistIP
	p.SignatureKey = nil

	reqHTTP, err := http.NewRequest("GET", "http://localhost/x114/http://google.com/a.jpg", strings.NewReader("request body"))
	if err != nil {
		b.Fatal(err)
	}
	req, err := NewRequest(reqHTTP, nil)
	if err != nil {
		b.Fatal(err)
	}

	allowedHosts.Purge()
	notAllowedHosts.Purge()

	// fill-in cache
	p.allowedCached(req, cname)

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		err := p.allowedCached(req, cname)
		if err == nil {
			b.Fatal("expected not allowed result")
		}
	}
}

func init() {
	log.SetOutput(ioutil.Discard)
}
