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
package imageproxy // import "github.com/wojtekzw/imageproxy"

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/wojtekzw/httpcache"
	"github.com/wojtekzw/imageproxy/ip"
	"github.com/wojtekzw/statsd"

	"image"
	_ "image/gif" // register gif format
	_ "image/jpeg"
	_ "image/png"

	"github.com/bluele/gcache"
	tphttp "github.com/wojtekzw/imageproxy/third_party/http"
)

const (
	// MaxRespBodySize - maximum size of remote image to be proxied. If image is larger Get(url) will return error.
	// It is safety feature to protect memory.
	MaxRespBodySize = 20 * 1024 * 1024

	// MaxPixels - maximum size of image in pixels.  If image is larger Get(url) will return error.
	// It is safety feature to protect memory.
	MaxPixels = 40 * 1000 * 1000

	// DebugMemoryLimit - memory usage above this limit will be logged to debug file and logs to statsd as separate event.
	DebugMemoryLimit = 3 * 1024 * 1024 * 1024

	// DateFormat - default format used in logging.
	DateFormat = "2006-01-02 15:04:05"

	// MaxConcurrency - max number of parallel image requests.
	MaxConcurrency = 15
)

var (
	concurrencyGuard = make(chan struct{}, MaxConcurrency)
	// Statsd - global statsd client to send metrics.
	Statsd statsd.Statser = &statsd.NoopClient{}
	// TODO
	memoryLastSeen uint64

	allowedHosts    = gcache.New(1024).LRU().Build()
	notAllowedHosts = gcache.New(1024).LRU().Build()
)

// Proxy serves image requests.
type Proxy struct {
	Client *http.Client // client used to fetch remote URLs
	Cache  Cache        // cache used to cache responses

	// Whitelist specifies a list of remote hosts that images can be
	// proxied from.  An empty list means all hosts are allowed.
	Whitelist []string

	// WhitelistIP specifies a list o allowed ranges of remote IPs that images can
	// be proxied from. An empty list means all hosts are allowed.
	WhitelistIP []ip.Range

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

	// Timeout specifies a time limit for requests served by this Proxy.
	// If a call runs for longer than its time limit, a 504 Gateway Timeout
	// response is returned.  A Timeout of zero means no timeout.
	Timeout time.Duration
}

// NewProxy constructs a new proxy.  The provided http RoundTripper will be
// used to fetch remote URLs.  If nil is provided, http.DefaultTransport will
// be used.
func NewProxy(transport http.RoundTripper, cache Cache, maxResponseSize uint64) *Proxy {
	//  TLSClientConfig: &tls.Config{InsecureSkipVerify: true},

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

// ServeHTTP handles incoming requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	Statsd.Increment("request.count.total")
	if r.URL.Path == "/favicon.ico" {
		Statsd.Increment("request.count.favicon")
		return // ignore favicon requests
	}

	if r.URL.Path == "/health" {
		if l := len(concurrencyGuard); l >= MaxConcurrency-2 {
			Statsd.Increment("request.count.health.too_many_req")
			w.WriteHeader(503)
			fmt.Fprintf(w, "error: too many concurrent image transform requests: %d", l)
			return
		}

		Statsd.Increment("request.count.health.ok")
		fmt.Fprint(w, "OK")
		return
	}

	if r.URL.Path == "/" {
		Statsd.Increment("request.count.root")
		fmt.Fprint(w, "OK")
		return
	}

	Statsd.Increment("request.count.image")

	var timer statsd.Timinger

	timer = Statsd.NewTiming()
	defer timer.Send("request.time")

	concurrencyGuard <- struct{}{}
	defer func() { <-concurrencyGuard }()

	lenCG := len(concurrencyGuard)
	Statsd.Gauge("concurrency", lenCG)

	var h http.Handler = http.HandlerFunc(p.serveImage)
	if p.Timeout > 0 {
		h = tphttp.TimeoutHandler(h, p.Timeout, "Gateway timeout waiting for remote resource.")
	}
	h.ServeHTTP(w, r)
}

// serveImage handles incoming requests for proxied images.
func (p *Proxy) serveImage(w http.ResponseWriter, r *http.Request) {

	req, err := NewRequest(r, p.DefaultBaseURL)
	if err != nil {
		msg := fmt.Sprintf("request: invalid URL: %v", err)
		glog.Error(msg)
		http.Error(w, msg, http.StatusBadRequest)
		Statsd.Increment("request.error.invalid_request_url")
		return
	}

	// assign static settings from proxy to req.Options
	req.Options.ScaleUp = p.ScaleUp

	if err = p.allowed(req, cname); err != nil {
		glog.Error(err)
		http.Error(w, err.Error(), http.StatusForbidden)
		Statsd.Increment("request.error.not_allowed")
		return
	}

	resp, err := p.Client.Get(req.String())
	if err != nil {
		msg := fmt.Sprintf("request: error fetching remote image: %v", err)
		glog.Error(msg)
		code := getStatusCode(err.Error())
		http.Error(w, msg, code)
		format := fmt.Sprintf("request.error.fetch.%d", code)
		Statsd.Increment("request.error.fetch")
		Statsd.Increment(format)
		return
	}
	defer resp.Body.Close()

	cached := resp.Header.Get(httpcache.XFromCache)
	glog.Infof("request: %v (served from cache: %v)", *req, cached == "1")
	memory := statsdProcessMemStats(Statsd)
	if memory.RSS > memoryLastSeen && memory.RSS >= DebugMemoryLimit {
		Statsd.Increment("memory.above_limit")
		memoryLastSeen = memory.RSS
	}
	if memory.RSS < memoryLastSeen {
		memoryLastSeen = memory.RSS
	}

	if cached == "1" {
		Statsd.Increment("request.cached")
	} else {
		Statsd.Increment("request.not_cached")
	}

	copyHeader(w.Header(), resp.Header, "Cache-Control", "Last-Modified", "Expires", "Etag", "Link")

	if should304(r, resp) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	copyHeader(w.Header(), resp.Header, "Content-Length", "Content-Type")

	//Enable CORS for 3rd party applications
	w.Header().Set("Access-Control-Allow-Origin", "*")

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)

	Statsd.Increment("request.code." + strconv.Itoa(resp.StatusCode))

}

// copyHeader copies header values from src to dst, adding to any existing
// values with the same header name.  If keys is not empty, only those header
// keys will be copied.
func copyHeader(dst, src http.Header, keys ...string) {
	if len(keys) == 0 {
		for k := range src {
			keys = append(keys, k)
		}
	}
	for _, key := range keys {
		k := http.CanonicalHeaderKey(key)
		for _, v := range src[k] {
			dst.Add(k, v)
		}
	}
}

// allowed determines whether the specified request contains an allowed
// referrer, host, and signature.  It returns an error if the request is not
// allowed.
func (p *Proxy) allowed(r *Request, cnFunc func(string) (string, error)) error {
	if len(p.Referrers) > 0 && !validReferrer(p.Referrers, r.Original) {
		return fmt.Errorf("request does not contain an allowed referrer: %v", r)
	}

	if len(p.Whitelist) == 0 && len(p.SignatureKey) == 0 && len(p.WhitelistIP) == 0 {
		return nil // no whitelist hosts or no whitelist IPs or signature key, all requests accepted
	}

	if (len(p.Whitelist) > 0 && validHostBool(p.Whitelist, r.URL, cnFunc)) || (len(p.WhitelistIP) > 0 && validIPBool(p.WhitelistIP, r.URL.Host)) {
		return nil // valid host OR valid IP
	}

	if len(p.SignatureKey) > 0 && validSignature(p.SignatureKey, r) {
		return nil
	}

	return fmt.Errorf("request does not contain an allowed host or valid signature: %v", r)
}

const (
	sUknown = iota
	sValid
	sInvalid
)

// allowed determines whether the specified request contains an allowed
// referrer, host, and signature.  It returns an error if the request is not
// allowed.
func (p *Proxy) allowedCached(r *Request, cnFunc func(string) (string, error)) error {
	if len(p.Referrers) > 0 && !validReferrer(p.Referrers, r.Original) {
		return fmt.Errorf("request does not contain an allowed referrer: %v", r)
	}

	if len(p.Whitelist) == 0 && len(p.SignatureKey) == 0 && len(p.WhitelistIP) == 0 {
		return nil // no whitelist hosts or no whitelist IPs or signature key, all requests accepted
	}

	if _, err := allowedHosts.Get(r.URL.Host); err == nil {
		return nil
	}

	// only if no signature we can check if dissallowed host - else signture can work
	if len(p.SignatureKey) == 0 || len(r.Options.Signature) == 0 {
		if _, err := notAllowedHosts.Get(r.URL.Host); err == nil {
			log.Printf("host hit in disallowed hosts cache: %s", r.URL.Host)
			return fmt.Errorf("request does not contain an allowed host: %s", r.URL.Host)
		}

	}

	if (len(p.Whitelist) > 0 && validHostBool(p.Whitelist, r.URL, cnFunc)) || (len(p.WhitelistIP) > 0 && validIPBool(p.WhitelistIP, r.URL.Host)) {
		allowedHosts.Set(r.URL.Host, struct{}{})
		return nil // valid host OR valid IP
	}

	if len(p.Whitelist) > 0 || len(p.WhitelistIP) > 0 {
		// invalid host becase was checked and we not returned from func
		if _, err := notAllowedHosts.Get(r.URL.Host); err != nil {
			notAllowedHosts.Set(r.URL.Host, struct{}{})
		}

	}

	if len(p.SignatureKey) > 0 && validSignature(p.SignatureKey, r) {
		return nil
	}

	return fmt.Errorf("request does not contain an allowed host or valid signature: %v", r)
}

func validIP(whitelistIPs []ip.Range, h string) (string, int) {

	// fmt.Fprints(os.Stderr,"valid IP host: %s\n",h)

	ips, err := net.LookupIP(h)
	if err != nil {
		log.Printf("error in lookup IP: %s, err: %v", h, err)
		return h, sInvalid
	}

	for _, hip := range ips {
		for _, wip := range whitelistIPs {
			if ip.Between(wip.From, wip.To, hip) {
				return h, sValid
			}
		}
	}
	return h, sInvalid
}

func validIPBool(whitelistIPs []ip.Range, h string) bool {
	_, v := validIP(whitelistIPs, h)
	return v == sValid
}

func cname(h string) (string, error) {
	c, err := net.LookupCNAME(h)
	if len(c) > 0 {
		c = c[:len(c)-1]
	}
	return c, err
}

// validHostSimple returns whether the host in u matches one of hosts.
func validHostSimple(hosts []string, h string) bool {
	if len(h) == 0 {
		return false
	}
	for _, host := range hosts {
		if h == host {
			return true
		}
		if strings.HasPrefix(host, "*.") && strings.HasSuffix(h, host[2:]) {
			return true
		}
	}

	return false
}

// validHostWithCNAME returns whether the host in u matches one of hosts or their CNAMEs.
func validHostWithCNAME(hosts []string, u *url.URL, cnFunc func(string) (string, error)) bool {

	if validHostSimple(hosts, u.Host) {
		return true
	}

	name, err := cnFunc(u.Host)

	if err == nil && validHostSimple(hosts, name) {
		return true
	}

	return false
}

func validHost(hosts []string, u *url.URL, cnFunc func(string) (string, error)) (string, int) {

	if validHostWithCNAME(hosts, u, cnFunc) {
		return u.Host, sValid
	}

	return u.Host, sInvalid
}

func validHostBool(hosts []string, u *url.URL, cnFunc func(string) (string, error)) bool {

	_, v := validHost(hosts, u, cnFunc)

	return v == sValid
}

// returns whether the referrer from the request is in the host list.
func validReferrer(hosts []string, r *http.Request) bool {
	u, err := url.Parse(r.Header.Get("Referer"))
	if err != nil { // malformed or blank header, just deny
		return false
	}

	return validHostSimple(hosts, u.Host)
}

// validSignature returns whether the request signature is valid.
func validSignature(key []byte, r *Request) bool {
	sig := r.Options.Signature
	if m := len(sig) % 4; m != 0 { // add padding if missing
		sig += strings.Repeat("=", 4-m)
	}

	got, err := base64.URLEncoding.DecodeString(sig)
	if err != nil {
		glog.Errorf("signature: error base64 decoding signature %q", r.Options.Signature)
		return false
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(r.URL.String()))
	want := mac.Sum(nil)

	return hmac.Equal(got, want)
}

// should304 returns whether we should send a 304 Not Modified in response to
// req, based on the response resp.  This is determined using the last modified
// time and the entity tag of resp.
func should304(req *http.Request, resp *http.Response) bool {
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

	// MaxResponseSize - maximum size of remote image to be be fetched by Client. If image is larger Get(url) will return error
	MaxResponseSize uint64
}

// RoundTrip implements the http.RoundTripper interface.
func (t *TransformingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Fragment == "" {
		// normal requests pass through
		return t.Transport.RoundTrip(req)
	}

	var timer statsd.Timinger

	timer = Statsd.NewTiming()

	u := *req.URL
	u.Fragment = ""
	resp, err := t.CachingClient.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("error in client request: %v", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}

	cts := resp.Header.Get("Content-Type")
	ct := strings.TrimSpace(strings.SplitN(cts, ";", 2)[0])
	switch ct {
	case "image/jpg", "image/jpeg", "image/png", "image/gif":
		break
	default:
		Statsd.Increment("image.error.invalid_content_type")
		return nil, fmt.Errorf("error: invalid content-type: %s => %s", cts, ct)
	}

	if should304(req, resp) {
		// bare 304 response, full response will be used from cache
		return &http.Response{StatusCode: http.StatusNotModified}, nil
	}

	contentLength, _ := strconv.Atoi(resp.Header.Get("Content-Length"))

	maxSize := t.MaxResponseSize
	// no data reading - check first Content-Length
	if uint64(contentLength) > maxSize {
		Statsd.Increment("image.error.too_large.bytes")
		return nil, fmt.Errorf("size in bytes too large: max size: %d, content-length: %d", maxSize,
			contentLength)
	}

	//read data with limiter if there is no Content-Length header or it is fake
	resp.Body = NewLimitedReadCloser(resp.Body, int64(maxSize))

	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		Statsd.Increment("image.error.read")
		return nil, err
	}
	timer.Send("request.get_image")
	Statsd.Gauge("request.size.bytes", len(b))

	//check image size in pixels
	imgReader := bytes.NewReader(b)
	imgCfg, _, err := image.DecodeConfig(imgReader)
	if err != nil {
		Statsd.Increment("image.error.invalid_format")
		return nil, fmt.Errorf("invalid image format: %v, url: %s", err, u.String())
	}

	pixels := imgCfg.Height * imgCfg.Width
	pixelSizeRatio := float64(pixels) / float64(MaxPixels)
	glog.Infof("image: %s, pixel height: %d, pixel width: %d, pixels: %d", u.String(),
		imgCfg.Height, imgCfg.Width, pixels)

	Statsd.Gauge("request.size.pixels", pixels)
	if pixels > MaxPixels {
		Statsd.Increment("image.error.too_large.pixels")
		return nil, fmt.Errorf("size in pixels too large: max size: %d, real size: %d, ratio: %.2f", MaxPixels,
			pixels, pixelSizeRatio)
	}

	opt := ParseOptions(req.URL.Fragment)

	var img []byte

	// if VipsEnabled {
	// 	img, err = Transform_VIPS(b, opt, u.String())
	// } else {
	// 	img, err = Transform(b, opt, u.String())
	// }
	img, err = Transform(b, opt, u.String())

	if err != nil {
		Statsd.Increment("image.error.transform")
		glog.Errorf("image: error transforming: %v, Content-Type: %v, URL: %v", err, resp.Header.Get("Content-Type"), req.URL)
		img = b
	}

	// replay response with transformed image and updated content length
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "%s %s\n", resp.Proto, resp.Status)
	resp.Header.WriteSubset(buf, map[string]bool{
		"Content-Length": true,
		// exclude Content-Type header if the format may have changed during transformation
		"Content-Type": opt.Format != "" || resp.Header.Get("Content-Type") == "image/webp" || resp.Header.Get("Content-Type") == "image/tiff",
	})
	fmt.Fprintf(buf, "Content-Length: %d\n\n", len(img))
	buf.Write(img)

	timer.Send("request.roundtrip.time")

	return http.ReadResponse(bufio.NewReader(buf), req)
}

var re = regexp.MustCompile(`status code:\s+(\d+)$`)

func getStatusCode(s string) int {
	var (
		code int
		err  error
	)
	// e := strings.Split(s,":")
	// c := strings.TrimSpace(e[len(e)-1])
	ss := re.FindStringSubmatch(s)
	if len(ss) == 2 {
		code, err = strconv.Atoi(ss[1])
		if err != nil {
			code = http.StatusInternalServerError
		}
	}

	if code == 0 || code > 599 {
		code = http.StatusInternalServerError
	}
	return code
}
