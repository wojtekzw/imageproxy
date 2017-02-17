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

// Package imageproxy provides an image proxy server.  For typical use of
// creating and using a Proxy, see cmd/imageproxy/main.go.
package imageproxy

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"
	"github.com/golang/glog"
	"github.com/wojtekzw/httpcache"
	"strconv"
	"github.com/wojtekzw/statsd"
	"os"

	"image"
	_ "image/gif" // register gif format
	_ "image/jpeg"
	_ "image/png"

)

const (
	// MaxRespBodySize - maximum size of remote image to be proxied. If image is larger Get(url) will return error
	// It is safety feature to protect memory
	MaxRespBodySize = 10 * 1024 * 1024

	// MaxPixels - maximum size of image in pixels.  If image is larger Get(url) will return error
	// It is safety feature to protect memory
	MaxPixels = 40 * 1000 * 1000

	DateFormat = "2006-01-02 15:04:05"
)

var (
	concurrencyGuard = make(chan struct{}, 15)
	Statsd statsd.Statser = &statsd.NoopClient{}
	DebugFile *os.File
)

// Proxy serves image requests.
type Proxy struct {
	Client *http.Client // client used to fetch remote URLs
	Cache  Cache        // cache used to cache responses

	// Whitelist specifies a list of remote hosts that images can be
	// proxied from.  An empty list means all hosts are allowed.
	Whitelist []string

	// Referrers, when given, requires that requests to the image
	// proxy come from a referring host. An empty list means all
	// hosts are allowed.
	Referrers []string

	// DefaultBaseURL is the URL that relative remote URLs are resolved in
	// reference to.  If nil, all remote URLs specified in requests must be
	// absolute.
	DefaultBaseURL *url.URL

	// SignatureKey is the HMAC key used to verify signed requests.
	SignatureKey []byte

	// Allow images to scale beyond their original dimensions.
	ScaleUp bool
}

// NewProxy constructs a new proxy.  The provided http RoundTripper will be
// used to fetch remote URLs.  If nil is provided, http.DefaultTransport will
// be used.
func NewProxy(transport http.RoundTripper, cache Cache, maxResponseSize uint64) *Proxy {
	if transport == nil {
		transport = http.DefaultTransport
	}
	if cache == nil {
		cache = NopCache
	}

	proxy := Proxy{
		Cache: cache,
	}

	client := new(http.Client)
	client.Transport = &httpcache.Transport{
		Transport:           &TransformingTransport{transport, client, maxResponseSize},
		Cache:               cache,
		MarkCachedResponses: true,
	}

	proxy.Client = client

	return &proxy
}

// ServeHTTP handles image requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	DebugFile.WriteString(time.Now().Format(DateFormat) + " "+ r.Host + r.RequestURI+"\n")
	DebugFile.Sync()

	glog.Infof("pre-request: %v", r.URL.String())


	Statsd.Increment("request.count.total")
	if r.URL.Path == "/favicon.ico" {
		Statsd.Increment("request.count.favicon")
		return // ignore favicon requests
	}

	if r.URL.Path == "/health-check" {
		Statsd.Increment("request.count.health_check")
		fmt.Fprint(w, "OK")
		return
	}


	var timer statsd.Timinger

	timer = Statsd.NewTiming()
	defer timer.Send("request.time")

	concurrencyGuard <- struct{}{}
	defer func() { <-concurrencyGuard }()

	lenCG := len(concurrencyGuard)
	Statsd.Gauge("concurrency",lenCG)



	glog.Infof("concurrency: %d",lenCG)




	req, err := NewRequest(r, p.DefaultBaseURL)
	if err != nil {
		msg := fmt.Sprintf("invalid request URL: %v", err)
		glog.Error(msg)
		http.Error(w, msg, http.StatusBadRequest)
		Statsd.Increment("request.error.invalid_request_url")
		return
	}

	// assign static settings from proxy to req.Options
	req.Options.ScaleUp = p.ScaleUp

	if err = p.allowed(req); err != nil {
		glog.Error(err)
		http.Error(w, err.Error(), http.StatusForbidden)
		Statsd.Increment("request.error.not_allowed")
		return
	}

	resp, err := p.Client.Get(req.String())
	if err != nil {
		msg := fmt.Sprintf("error fetching remote image: %v", err)
		glog.Error(msg)
		http.Error(w, msg, http.StatusInternalServerError)
		Statsd.Increment("request.error.fetch")
		return
	}
	defer resp.Body.Close()

	cached := resp.Header.Get(httpcache.XFromCache)
	glog.Infof("request: %v (served from cache: %v)", *req, cached == "1")
	memory := statsdProcessMemStats(Statsd)
	if memory.RSS >= 1024*1024*1024 {
		DebugFile.WriteString("# " + time.Now().Format(DateFormat) + " memory RSS: "+ fmt.Sprintf("%d",memory.RSS) +"\n")
		DebugFile.Sync()
	}


	if cached == "1" {
		Statsd.Increment("request.cached")
	} else {
		Statsd.Increment("request.not_cached")
	}

	copyHeader(w, resp, "Cache-Control")
	copyHeader(w, resp, "Last-Modified")
	copyHeader(w, resp, "Expires")
	copyHeader(w, resp, "Etag")
	copyHeader(w, resp, "Link")

	if is304 := check304(r, resp); is304 {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	copyHeader(w, resp, "Content-Length")
	copyHeader(w, resp, "Content-Type")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
	Statsd.Increment("request.code."+ strconv.Itoa(resp.StatusCode))
}

func copyHeader(w http.ResponseWriter, r *http.Response, header string) {
	key := http.CanonicalHeaderKey(header)
	if value, ok := r.Header[key]; ok {
		w.Header()[key] = value
	}
}

// allowed determines whether the specified request contains an allowed
// referrer, host, and signature.  It returns an error if the request is not
// allowed.
func (p *Proxy) allowed(r *Request) error {
	if len(p.Referrers) > 0 && !validReferrer(p.Referrers, r.Original) {
		return fmt.Errorf("request does not contain an allowed referrer: %v", r)
	}

	if len(p.Whitelist) == 0 && len(p.SignatureKey) == 0 {
		return nil // no whitelist or signature key, all requests accepted
	}

	if len(p.Whitelist) > 0 && validHost(p.Whitelist, r.URL) {
		return nil
	}

	if len(p.SignatureKey) > 0 && validSignature(p.SignatureKey, r) {
		return nil
	}

	return fmt.Errorf("request does not contain an allowed host or valid signature: %v", r)
}

// validHost returns whether the host in u matches one of hosts.
func validHost(hosts []string, u *url.URL) bool {
	for _, host := range hosts {
		if u.Host == host {
			return true
		}
		if strings.HasPrefix(host, "*.") && strings.HasSuffix(u.Host, host[2:]) {
			return true
		}
	}

	return false
}

// returns whether the referrer from the request is in the host list.
func validReferrer(hosts []string, r *http.Request) bool {
	u, err := url.Parse(r.Header.Get("Referer"))
	if err != nil { // malformed or blank header, just deny
		return false
	}

	return validHost(hosts, u)
}

// validSignature returns whether the request signature is valid.
func validSignature(key []byte, r *Request) bool {
	sig := r.Options.Signature
	if m := len(sig) % 4; m != 0 { // add padding if missing
		sig += strings.Repeat("=", 4-m)
	}

	got, err := base64.URLEncoding.DecodeString(sig)
	if err != nil {
		glog.Errorf("error base64 decoding signature %q", r.Options.Signature)
		return false
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(r.URL.String()))
	want := mac.Sum(nil)

	return hmac.Equal(got, want)
}

// check304 checks whether we should send a 304 Not Modified in response to
// req, based on the response resp.  This is determined using the last modified
// time and the entity tag of resp.
func check304(req *http.Request, resp *http.Response) bool {
	// TODO(willnorris): if-none-match header can be a comma separated list
	// of multiple tags to be matched, or the special value "*" which
	// matches all etags
	etag := resp.Header.Get("Etag")
	if etag != "" && etag == req.Header.Get("If-None-Match") {
		return true
	}

	lastModified, err := time.Parse(time.RFC1123, resp.Header.Get("Last-Modified"))
	if err != nil {
		return false
	}
	ifModSince, err := time.Parse(time.RFC1123, req.Header.Get("If-Modified-Since"))
	if err != nil {
		return false
	}
	if lastModified.Before(ifModSince) {
		return true
	}

	return false
}

// TransformingTransport is an implementation of http.RoundTripper that
// optionally transforms images using the options specified in the request URL
// fragment.
type TransformingTransport struct {
	// Transport is the underlying http.RoundTripper used to satisfy
	// non-transform requests (those that do not include a URL fragment).
	Transport http.RoundTripper

	// CachingClient is used to fetch images to be resized.  This client is
	// used rather than Transport directly in order to ensure that
	// responses are properly cached.
	CachingClient *http.Client

	// ResponseSize - maximum size of remote image to be be fetched by Client. If image is larger Get(url) will return error
	ResponseSize uint64
}

// RoundTrip implements the http.RoundTripper interface.
func (t *TransformingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Fragment == "" {
		// normal requests pass through
		glog.Infof("fetching remote URL: %v", req.URL)
		return t.Transport.RoundTrip(req)
	}


	var timer statsd.Timinger

	timer = Statsd.NewTiming()

	u := *req.URL
	u.Fragment = ""
	resp, err := t.CachingClient.Get(u.String())
	if err != nil {
		return nil, err
	}


	contentLength, _ := strconv.Atoi(resp.Header.Get("Content-Length"))
	// no data reading - check first Content-Length
	if uint64(contentLength) > t.ResponseSize {
		Statsd.Increment("image.error.too_large.bytes")
		return nil, fmt.Errorf("size in bytes too large: max size: %d, content-length: %d",t.ResponseSize,
			contentLength)
	}

	//read data with limiter if there is no Content-Length header or it is fake
	resp.Body = NewLimitedReadCloser(resp.Body, int64(t.ResponseSize))
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		Statsd.Increment("image.error.read")
		return nil, err
	}
	timer.Send("request.get_image")
	Statsd.Gauge("request.size.bytes",len(b))


	//check image size in pixels
	imgReader := bytes.NewReader(b)
	imgCfg, _, err := image.DecodeConfig(imgReader)
	pixels := imgCfg.Height*imgCfg.Width
	pixelSizeRatio := float64(pixels)/float64(MaxPixels)
	glog.Infof("image: %s, pixel height: %d, pixel width: %d, pixels: %d", u.String(),
		imgCfg.Height,imgCfg.Width,pixels)

	Statsd.Gauge("request.size.pixels",pixels)
	if pixels > MaxPixels {
		Statsd.Increment("image.error.too_large.pixels")
		return nil, fmt.Errorf("size in pixels too large: max size: %d, real size: %d, ratio: %.2f",MaxPixels,
			pixels, pixelSizeRatio)
	}

	opt := ParseOptions(req.URL.Fragment)

	img, err := Transform(b, opt,u.String())
	if err != nil {
		Statsd.Increment("image.error.transform")
		glog.Errorf("error transforming image: %v, Content-Type: %v, URL: %v", err, resp.Header.Get("Content-Type"),req.URL)
		img = b
	}

	// replay response with transformed image and updated content length
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "%s %s\n", resp.Proto, resp.Status)
	resp.Header.WriteSubset(buf, map[string]bool{"Content-Length": true})
	fmt.Fprintf(buf, "Content-Length: %d\n\n", len(img))
	buf.Write(img)

	return http.ReadResponse(bufio.NewReader(buf), req)
}
